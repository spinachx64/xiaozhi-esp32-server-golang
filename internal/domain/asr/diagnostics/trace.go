// Package diagnostics captures bounded ASR traces for local troubleshooting.
package diagnostics

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	log "xiaozhi-esp32-server-golang/logger"
)

type contextKey struct{}

var sequence atomic.Uint64
var saves = make(chan *Trace, 2)

func init() {
	go func() {
		for t := range saves {
			t.save()
		}
	}()
}

type event struct {
	AtMS    int64  `json:"at_ms"`
	Message string `json:"message"`
}

// Trace retains at most 60 seconds per audio stream and 512 timeline events.
// Capturing is opt-in and limited to the first 100 turns per process.
type Trace struct {
	mu             sync.Mutex
	ID             string
	dir            string
	rate, channels int
	started        time.Time
	closed         bool
	audio          map[string][]byte
	truncated      map[string]bool
	events         []event
}

func New(ctx context.Context, device string, rate, channels int) (context.Context, *Trace) {
	n := sequence.Add(1)
	device = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, device)
	t := &Trace{ID: fmt.Sprintf("%s-%s-%03d", time.Now().Format("20060102-150405.000"), device, n), rate: rate, channels: channels, started: time.Now()}
	if dir := os.Getenv("XIAOZHI_ASR_DEBUG_DIR"); dir != "" && n <= 100 && rate > 0 && channels > 0 {
		t.dir = filepath.Join(dir, t.ID)
		t.audio = make(map[string][]byte)
		t.truncated = make(map[string]bool)
	}
	return context.WithValue(ctx, contextKey{}, t), t
}

func FromContext(ctx context.Context) *Trace {
	t, _ := ctx.Value(contextKey{}).(*Trace)
	return t
}

func (t *Trace) Debugf(format string, args ...interface{}) {
	if t == nil {
		log.Debugf(format, args...)
		return
	}
	message := fmt.Sprintf(format, args...)
	log.Debugf("ASR trace=%s %s", t.ID, message)
	t.Event(message)
}

func (t *Trace) Event(message string) {
	if t == nil || t.dir == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.closed {
		// Preserve the most recent events, including commit and final, on long turns.
		if len(t.events) == 512 {
			copy(t.events, t.events[1:])
			t.events = t.events[:511]
		}
		t.events = append(t.events, event{time.Since(t.started).Milliseconds(), message})
	}
}

// PCM stores a copy; callers may reuse their frame buffers immediately.
func (t *Trace) PCM(stream string, samples []float32) {
	if t == nil || t.dir == "" {
		return
	}
	data := make([]byte, len(samples)*2)
	for i, v := range samples {
		if math.IsNaN(float64(v)) {
			v = 0
		}
		v = max(-1, min(1, v))
		binary.LittleEndian.PutUint16(data[i*2:], uint16(int16(v*32767)))
	}
	t.Bytes(stream, data)
}

func (t *Trace) Bytes(stream string, data []byte) {
	if t == nil || t.dir == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	remaining := t.rate*t.channels*2*60 - len(t.audio[stream])
	if len(data) > remaining {
		t.truncated[stream] = true
		data = data[:remaining]
	}
	t.audio[stream] = append(t.audio[stream], data...)
}

func (t *Trace) Close() {
	if t == nil || t.dir == "" {
		return
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	t.mu.Unlock()
	select {
	case saves <- t:
	default:
		log.Warnf("ASR trace=%s debug writer busy, capture discarded", t.ID)
	}
}

func wav(data []byte, rate, channels int) []byte {
	b := make([]byte, 44+len(data))
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+len(data)))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], uint16(channels))
	binary.LittleEndian.PutUint32(b[24:], uint32(rate))
	binary.LittleEndian.PutUint32(b[28:], uint32(rate*channels*2))
	binary.LittleEndian.PutUint16(b[32:], uint16(channels*2))
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(len(data)))
	copy(b[44:], data)
	return b
}

func (t *Trace) save() {
	if err := os.MkdirAll(t.dir, 0700); err != nil {
		log.Warnf("ASR trace=%s mkdir: %v", t.ID, err)
		return
	}
	for name, data := range t.audio {
		if err := os.WriteFile(filepath.Join(t.dir, name+".wav"), wav(data, t.rate, t.channels), 0600); err != nil {
			log.Warnf("ASR trace=%s write %s: %v", t.ID, name, err)
		}
	}
	metadata := struct {
		ID         string          `json:"id"`
		SampleRate int             `json:"sample_rate"`
		Channels   int             `json:"channels"`
		Truncated  map[string]bool `json:"truncated"`
		Events     []event         `json:"events"`
	}{t.ID, t.rate, t.channels, t.truncated, t.events}
	b, err := json.MarshalIndent(metadata, "", "  ")
	if err == nil {
		err = os.WriteFile(filepath.Join(t.dir, "events.json"), b, 0600)
	}
	if err != nil {
		log.Warnf("ASR trace=%s write events: %v", t.ID, err)
		return
	}
	log.Infof("ASR trace=%s capture saved: %s", t.ID, t.dir)
}
