package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"xiaozhi-esp32-server-golang/internal/app/server/chat"
	"xiaozhi-esp32-server-golang/internal/domain/delegate"
	log "xiaozhi-esp32-server-golang/logger"

	"github.com/spf13/viper"
)

const (
	delegateIdleChecks     = 3 // consecutive idle polls before announcing
	delegateMaxInterrupted = 3 // per result and connection; afterwards wait for the next attach
	delegateMaxInjectFails = 3
	delegateMaxBusy        = 20 // busy replies per connection before waiting for the next attach
)

// Variables so tests can shorten them.
var (
	delegateIdlePoll     = 300 * time.Millisecond
	delegateSpeakTimeout = 3 * time.Minute
	delegateRetryDelay   = 2 * time.Second
)

// speechTarget is the part of a device connection the delivery loop needs;
// *chat.ChatManager implements it.
type speechTarget interface {
	IsConversationActive() bool
	InjectDelegateSpeech(text string, onSpoken func(played bool)) error
}

// delegateDelivery announces background task results. Results stay on the
// agent bridge until they have been played to the end, so a device that is
// offline, busy or interrupts the announcement hears them later.
type delegateDelivery struct {
	lookup func(deviceID string) (speechTarget, bool)
	mgr    *delegate.Manager

	mu     sync.Mutex
	active map[string]bool // a delivery loop runs for the device
	again  map[string]bool // new result arrived while the loop ran
}

func newDelegateDeliveryWith(mgr *delegate.Manager, lookup func(deviceID string) (speechTarget, bool)) *delegateDelivery {
	return &delegateDelivery{lookup: lookup, mgr: mgr, active: map[string]bool{}, again: map[string]bool{}}
}

// newDelegateDelivery returns nil when delegate.enabled is false.
func newDelegateDelivery(app *App) (*delegateDelivery, error) {
	if !viper.GetBool("delegate.enabled") {
		return nil, nil
	}
	token := viper.GetString("delegate.token")
	if token == "" {
		token = os.Getenv("AGENT_BRIDGE_TOKEN")
	}
	client, err := delegate.NewClient(viper.GetString("delegate.bridge_url"), token)
	if err != nil {
		return nil, err
	}
	timeout := viper.GetDuration("delegate.task_timeout")
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	mgr := delegate.NewManager(client, timeout, viper.GetInt("delegate.max_concurrent"))
	mgr.SetLogger(func(msg string) { log.Warnf("[delegate] %s", msg) })
	return newDelegateDeliveryWith(mgr, func(deviceID string) (speechTarget, bool) {
		cm, ok := app.GetChatManager(deviceID)
		if !ok || cm == nil {
			return nil, false
		}
		return cm, true
	}), nil
}

// attach is called for each new ChatManager; the returned func on its end.
func (d *delegateDelivery) attach(deviceID string) func() {
	if d == nil {
		return func() {}
	}
	return d.mgr.Attach(deviceID, func() { d.kick(deviceID) })
}

// kick starts the device's delivery loop, or tells the running one to check again.
func (d *delegateDelivery) kick(deviceID string) {
	d.mu.Lock()
	if d.active[deviceID] {
		d.again[deviceID] = true
		d.mu.Unlock()
		return
	}
	d.active[deviceID] = true
	d.mu.Unlock()

	go func() {
		for {
			d.deliverAll(deviceID)
			d.mu.Lock()
			if d.again[deviceID] {
				d.again[deviceID] = false
				d.mu.Unlock()
				continue
			}
			delete(d.active, deviceID)
			d.mu.Unlock()
			return
		}
	}()
}

func (d *delegateDelivery) deliverAll(deviceID string) {
	interrupted := map[string]int{}
	injectFails := 0
	busy := 0
	for d.mgr.HasPending(deviceID) {
		cm, ok := d.waitIdle(deviceID)
		if !ok {
			return // offline; the next attach kicks again
		}
		r, ok := d.mgr.TakePending(deviceID)
		if !ok {
			return
		}
		if interrupted[r.TaskID] >= delegateMaxInterrupted {
			d.mgr.Requeue(deviceID, r)
			return
		}

		played := make(chan bool, 1)
		err := cm.InjectDelegateSpeech(delegate.Announcement(r), func(ok bool) { played <- ok })
		if errors.Is(err, chat.ErrSpeakBusy) {
			// The device was in a conversation after all (e.g. the user just
			// pressed the button). Not a failure: wait for idle and try again.
			d.mgr.Requeue(deviceID, r)
			busy++
			log.Infof("[delegate] device=%s task=%s device busy (%d), retry when idle", deviceID, r.TaskID, busy)
			if busy >= delegateMaxBusy {
				return
			}
			time.Sleep(delegateRetryDelay)
			continue
		}
		if err != nil {
			d.mgr.Requeue(deviceID, r)
			injectFails++
			log.Warnf("[delegate] device=%s task=%s announce failed (%d): %v", deviceID, r.TaskID, injectFails, err)
			if injectFails >= delegateMaxInjectFails {
				return
			}
			time.Sleep(delegateRetryDelay)
			continue
		}
		injectFails = 0

		select {
		case ok := <-played:
			if ok {
				log.Infof("[delegate] device=%s task=%s announced", deviceID, r.TaskID)
				d.mgr.Delivered(deviceID, r.TaskID)
				continue
			}
			// Interrupted (user spoke, session closed): keep it and retry once
			// the conversation is over.
			interrupted[r.TaskID]++
			log.Infof("[delegate] device=%s task=%s announcement interrupted (%d)", deviceID, r.TaskID, interrupted[r.TaskID])
			d.mgr.Requeue(deviceID, r)
		case <-time.After(delegateSpeakTimeout):
			log.Warnf("[delegate] device=%s task=%s announcement did not finish in time", deviceID, r.TaskID)
			d.mgr.Requeue(deviceID, r)
			return
		}
	}
}

// waitIdle blocks until the device is connected and not in a conversation for
// several polls in a row. It returns false once the device is offline.
func (d *delegateDelivery) waitIdle(deviceID string) (speechTarget, bool) {
	idle := 0
	for {
		cm, ok := d.lookup(deviceID)
		if !ok || cm == nil {
			return nil, false
		}
		if cm.IsConversationActive() {
			idle = 0
		} else if idle++; idle >= delegateIdleChecks {
			return cm, true
		}
		time.Sleep(delegateIdlePoll)
	}
}

// ---------------------------------------------------------------------------
// HTTP API: deterministic task entry until the LLM routes tasks itself.
//
//	POST /api/tasks         {"device_id","text","context"} -> {"task_id"}
//	GET  /api/tasks?device_id=...                         -> running + recent
//	POST /api/tasks/cancel  {"device_id","task_id"}       -> {"canceled"}
//
// All routes require "Authorization: Bearer <admin token>".
// ---------------------------------------------------------------------------

// adminToken is shared by the task API and /admin/inject_msg.
func adminToken() string {
	if t := strings.TrimSpace(viper.GetString("admin_api.token")); t != "" {
		return t
	}
	return strings.TrimSpace(os.Getenv("XIAOZHI_ADMIN_TOKEN"))
}

// checkAdminToken writes 401/503 and returns false unless the request carries
// the configured token. Without a token the routes are disabled.
func checkAdminToken(w http.ResponseWriter, r *http.Request) bool {
	want := adminToken()
	if want == "" {
		http.Error(w, "admin api disabled: set admin_api.token", http.StatusServiceUnavailable)
		return false
	}
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

func (d *delegateDelivery) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/tasks", d.handleTasks)
	mux.HandleFunc("/api/tasks/cancel", d.handleCancel)
}

func (d *delegateDelivery) handleTasks(w http.ResponseWriter, r *http.Request) {
	if !checkAdminToken(w, r) {
		return
	}
	switch r.Method {
	case http.MethodPost:
		var req struct {
			DeviceID string `json:"device_id"`
			Text     string `json:"text"`
			Context  string `json:"context"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.DeviceID) == "" || strings.TrimSpace(req.Text) == "" {
			http.Error(w, "device_id and text are required", http.StatusBadRequest)
			return
		}
		id, err := d.mgr.Submit(req.DeviceID, req.Text, req.Context)
		if errors.Is(err, delegate.ErrTooManyTasks) {
			http.Error(w, err.Error(), http.StatusTooManyRequests)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		log.Infof("[delegate] device=%s task=%s submitted", req.DeviceID, id)
		writeJSON(w, http.StatusAccepted, map[string]any{"task_id": id})
	case http.MethodGet:
		deviceID := r.URL.Query().Get("device_id")
		if deviceID == "" {
			http.Error(w, "device_id is required", http.StatusBadRequest)
			return
		}
		running, recent := d.mgr.Status(deviceID)
		type finished struct {
			TaskID   string    `json:"task_id"`
			Task     string    `json:"task"`
			OK       bool      `json:"ok"`
			Finished time.Time `json:"finished_at"`
		}
		out := make([]finished, 0, len(recent))
		for _, res := range recent {
			out = append(out, finished{TaskID: res.TaskID, Task: res.Task, OK: res.Err == nil, Finished: res.Finished})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"running":        running,
			"recent":         out,
			"pending_speech": d.mgr.HasPending(deviceID),
		})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (d *delegateDelivery) handleCancel(w http.ResponseWriter, r *http.Request) {
	if !checkAdminToken(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		DeviceID string `json:"device_id"`
		TaskID   string `json:"task_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil || req.DeviceID == "" {
		http.Error(w, "device_id is required", http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"canceled": d.mgr.Cancel(req.DeviceID, req.TaskID)})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
