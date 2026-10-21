package chat

import (
	"errors"
	"time"

	. "xiaozhi-esp32-server-golang/internal/data/client"
	log "xiaozhi-esp32-server-golang/logger"
)

// ErrSpeakBusy means the device answered speak_request with "busy": it is in
// a conversation and the announcement should be retried once it is idle.
var ErrSpeakBusy = errors.New("device busy, speak request rejected")

// speakDoneTimeout bounds the wait for the device's speak_done after the
// server finished sending an announcement.
var speakDoneTimeout = 15 * time.Second

// speakDoneWaiter waits for the speak_done of one announcement. It is armed
// only when the device accepted a speak_request for it; announcements sent
// over an already open channel get no speak_done.
type speakDoneWaiter struct {
	armed bool
	ch    chan int // played frames
}

// InjectDelegateSpeech speaks a background task result without the LLM.
// onSpoken is called once after the turn ends: played is false when the user
// interrupted, the session closed, no audio was produced, or the device
// reported that nothing reached the speaker. It is not called when the text
// could not be queued; the returned error says so (ErrSpeakBusy included).
func (c *ChatManager) InjectDelegateSpeech(text string, onSpoken func(played bool)) error {
	c.cancelRetainedSessionCleanup("delegate_result")
	waiter := c.newSpeakDoneWaiter()
	if err := c.prepareSpeakPathForInjectedSpeech(text, false); err != nil {
		c.dropSpeakDoneWaiter(waiter)
		return err
	}
	session, err := c.ensureSession()
	if err != nil {
		c.dropSpeakDoneWaiter(waiter)
		return err
	}
	err = session.AddTextToTTSQueueWithOptions(text, llmResponseChannelOptions{
		onTTSPlaybackStart: c.newInjectedSpeechStartHook(),
		ttsTurnEndPolicy:   injectedSpeechTTSTurnEndPolicy(false),
		onSpoken:           func(played bool) { c.confirmSpoken(waiter, played, onSpoken) },
	})
	if err != nil {
		c.dropSpeakDoneWaiter(waiter)
	}
	return err
}

// confirmSpoken reports the server-side result, or, when the device accepted
// a speak_request, waits for its speak_done and requires played frames.
func (c *ChatManager) confirmSpoken(w *speakDoneWaiter, played bool, onSpoken func(bool)) {
	c.speakDoneMu.Lock()
	armed := w != nil && w.armed
	c.speakDoneMu.Unlock()
	if !armed {
		c.dropSpeakDoneWaiter(w)
		onSpoken(played)
		return
	}
	go func() {
		defer c.dropSpeakDoneWaiter(w)
		select {
		case frames := <-w.ch:
			if played && frames == 0 {
				log.Warnf("设备 %s speak_done 报告 0 帧播放，视为未播出", c.DeviceID)
			}
			onSpoken(played && frames > 0)
		case <-time.After(speakDoneTimeout):
			log.Warnf("设备 %s 等待 speak_done 超时，视为未播出", c.DeviceID)
			onSpoken(false)
		}
	}()
}

// newSpeakDoneWaiter always creates a waiter: support for speak_done is only
// known from the device's hello, which may arrive during the speak_request
// handshake. armSpeakDone checks support at speak_ready time.
func (c *ChatManager) newSpeakDoneWaiter() *speakDoneWaiter {
	w := &speakDoneWaiter{ch: make(chan int, 1)}
	c.speakDoneMu.Lock()
	c.speakDone = w
	c.speakDoneMu.Unlock()
	return w
}

func (c *ChatManager) dropSpeakDoneWaiter(w *speakDoneWaiter) {
	if w == nil {
		return
	}
	c.speakDoneMu.Lock()
	if c.speakDone == w {
		c.speakDone = nil
	}
	c.speakDoneMu.Unlock()
}

// armSpeakDone is called when the device accepts a speak_request. The hello
// of that handshake has been handled by then, so the feature flag is current.
func (c *ChatManager) armSpeakDone() {
	if !c.speakDoneSupported.Load() {
		return
	}
	c.speakDoneMu.Lock()
	if c.speakDone != nil {
		c.speakDone.armed = true
	}
	c.speakDoneMu.Unlock()
}

func (c *ChatManager) handleSpeakDoneMessage(msg *ClientMessage) {
	log.Infof("设备 %s speak_done: packets=%d played_frames=%d", c.DeviceID, msg.Packets, msg.PlayedFrames)
	c.speakDoneMu.Lock()
	w := c.speakDone
	c.speakDoneMu.Unlock()
	if w == nil || !w.armed {
		return
	}
	select {
	case w.ch <- msg.PlayedFrames:
	default:
	}
}

// IsConversationActive reports whether the device is listening, thinking or
// speaking, so background announcements can wait for the turn to end.
func (c *ChatManager) IsConversationActive() bool {
	return c.isConversationActive()
}
