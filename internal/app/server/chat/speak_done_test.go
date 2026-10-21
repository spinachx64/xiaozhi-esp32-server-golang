package chat

import (
	"context"
	"errors"
	"testing"
	"time"

	types_conn "xiaozhi-esp32-server-golang/internal/app/server/types"
	data_client "xiaozhi-esp32-server-golang/internal/data/client"
	msgdata "xiaozhi-esp32-server-golang/internal/data/msg"
)

func TestSpeakReadyBusyFailsPendingRequest(t *testing.T) {
	manager, _ := newSpeakRequestTestManager(types_conn.TransportTypeMqttUdp)
	manager.helloInited = true
	pending, _ := manager.getOrCreatePendingSpeakRequest()

	if err := manager.HandleSpeakReadyMessage(&data_client.ClientMessage{
		Type:      msgdata.MessageTypeSpeakReady,
		SessionID: pending.sessionID,
		State:     msgdata.MessageStateBusy,
	}); err != nil {
		t.Fatalf("HandleSpeakReadyMessage returned error: %v", err)
	}
	if err := manager.waitPendingSpeakRequest(context.Background(), pending); !errors.Is(err, ErrSpeakBusy) {
		t.Fatalf("pending speak_request error = %v, want ErrSpeakBusy", err)
	}
}

// confirmSpokenResult runs confirmSpoken for an announcement the device
// accepted and returns what onSpoken reported.
func confirmSpokenResult(t *testing.T, played bool, sendDone func(c *ChatManager)) bool {
	t.Helper()
	c := &ChatManager{}
	c.speakDoneSupported.Store(true)
	w := c.newSpeakDoneWaiter()
	c.armSpeakDone()
	got := make(chan bool, 1)
	c.confirmSpoken(w, played, func(p bool) { got <- p })
	if sendDone != nil {
		sendDone(c)
	}
	select {
	case p := <-got:
		return p
	case <-time.After(time.Second):
		t.Fatal("onSpoken was not called")
		return false
	}
}

func speakDone(frames int) func(c *ChatManager) {
	return func(c *ChatManager) {
		c.handleSpeakDoneMessage(&data_client.ClientMessage{Type: msgdata.MessageTypeSpeakDone, PlayedFrames: frames})
	}
}

func TestConfirmSpokenRequiresPlayedFrames(t *testing.T) {
	if !confirmSpokenResult(t, true, speakDone(42)) {
		t.Error("played frames reported: want played")
	}
	if confirmSpokenResult(t, true, speakDone(0)) {
		t.Error("device reported 0 frames: want not played")
	}
	if confirmSpokenResult(t, false, speakDone(42)) {
		t.Error("server-side interruption: want not played")
	}
}

func TestConfirmSpokenTimesOutWithoutSpeakDone(t *testing.T) {
	old := speakDoneTimeout
	speakDoneTimeout = 20 * time.Millisecond
	t.Cleanup(func() { speakDoneTimeout = old })
	if confirmSpokenResult(t, true, nil) {
		t.Error("no speak_done: want not played")
	}
}

// Firmware without the speak_done feature keeps the server-side result.
func TestConfirmSpokenWithoutSpeakDoneSupport(t *testing.T) {
	c := &ChatManager{}
	// Support is unknown until the handshake's hello, so a waiter is always
	// created; it stays unarmed when the hello did not announce speak_done.
	w := c.newSpeakDoneWaiter()
	c.armSpeakDone()
	if w.armed {
		t.Fatal("waiter armed although speak_done is unsupported")
	}
	got := make(chan bool, 1)
	c.confirmSpoken(w, true, func(p bool) { got <- p })
	if !<-got {
		t.Error("want played when the device cannot report speak_done")
	}
}
