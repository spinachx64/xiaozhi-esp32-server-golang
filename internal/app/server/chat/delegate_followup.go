package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"xiaozhi-esp32-server-golang/internal/domain/llm"
	llm_common "xiaozhi-esp32-server-golang/internal/domain/llm/common"
	"xiaozhi-esp32-server-golang/internal/pool"
	log "xiaozhi-esp32-server-golang/logger"

	"github.com/cloudwego/eino/schema"
)

// Background task results that finish while the user is talking: the device
// gets a task_ready notice (icon + sound), the next LLM turn is told to offer
// the result, and play_task_result speaks it inside the conversation.

// taskHints holds a one-shot system prompt addition per device.
var taskHints sync.Map // deviceID -> string

// SetTaskHint makes the next LLM turn of the device mention a finished task.
func SetTaskHint(deviceID, hint string) {
	if hint == "" {
		taskHints.Delete(deviceID)
		return
	}
	taskHints.Store(deviceID, hint)
}

// takeTaskHint returns and clears the device's hint.
func takeTaskHint(deviceID string) string {
	v, ok := taskHints.LoadAndDelete(deviceID)
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// SendTaskReady tells the device a background task finished, without
// interrupting the conversation.
func (c *ChatManager) SendTaskReady(taskID, task string) error {
	if c == nil || c.serverTransport == nil {
		return errors.New("no transport")
	}
	b, err := json.Marshal(map[string]string{"type": "task_ready", "task_id": taskID, "task": task})
	if err != nil {
		return err
	}
	return c.serverTransport.SendCmd(b)
}

// speakInTurnTimeout bounds SpeakInTurn; a 150-character summary takes ~40s.
var speakInTurnTimeout = 3 * time.Minute

// SpeakInTurn speaks text as part of the LLM turn ctx belongs to, e.g. from a
// local MCP tool. The turn's tts stop is sent only after the tool returns, so
// the device keeps playing instead of switching to listening (a separate turn
// queued after the tool would be cancelled by that listen start). It blocks
// until the text was spoken or the turn was interrupted.
func (c *ChatManager) SpeakInTurn(ctx context.Context, text string) (bool, error) {
	session := c.GetSession()
	if session == nil || session.ttsManager == nil {
		return false, errors.New("no active conversation")
	}
	// Own tracker: audio and errors of this text only, not of the whole turn.
	tracker := newTTSTurnTracker()
	ctx = context.WithValue(ctx, ttsTurnTrackerKey, tracker)
	done := make(chan struct{})
	var once sync.Once
	queued := false
	onEnqueued := func() func(error) {
		queued = true
		end := tracker.Add()
		return func(err error) {
			end(err)
			once.Do(func() { close(done) })
		}
	}
	resp := llm_common.LLMResponseStruct{Text: text, IsStart: true, IsEnd: true}
	if err := session.ttsManager.handleTextResponseWithHooks(ctx, resp, false, onEnqueued, nil); err != nil {
		return false, err
	}
	if !queued {
		return false, nil // turn already cancelled or a hook dropped the text
	}
	timer := time.NewTimer(speakInTurnTimeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-ctx.Done():
		return false, nil
	case <-timer.C:
		return false, errors.New("speech did not finish in time")
	}
	return ctx.Err() == nil && tracker.Err() == nil && tracker.HadAudio(), nil
}

const compressPrompt = "你是手表上的语音助手。下面是后台任务的结果，请把它改写成给用户听的口语摘要：" +
	"先说结论，总共不超过 %d 个字，不要使用 markdown、列表、emoji，只输出摘要本身。"

// CompressForSpeech shortens a long task result with the device's LLM.
func (c *ChatManager) CompressForSpeech(ctx context.Context, task, text string, maxRunes int) (string, error) {
	if c == nil || c.clientState == nil {
		return "", errors.New("no client state")
	}
	cfg := c.clientState.DeviceConfig.Llm
	if cfg.Provider == "" {
		return "", errors.New("no LLM configured")
	}
	w, err := pool.Acquire[llm.LLMProvider]("llm", cfg.Provider, cfg.Config)
	if err != nil {
		return "", err
	}
	defer pool.Release(w)

	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	dialogue := []*schema.Message{
		{Role: schema.System, Content: fmt.Sprintf(compressPrompt, maxRunes)},
		{Role: schema.User, Content: "任务：" + task + "\n\n结果：\n" + text},
	}
	var b strings.Builder
	for msg := range w.GetProvider().ResponseWithContext(ctx, "delegate-compress", dialogue, nil) {
		if llm.IsLLMErrorMessage(msg) {
			return "", errors.New(llm.LLMErrorMessage(msg))
		}
		if msg != nil {
			b.WriteString(msg.Content)
		}
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return "", errors.New("empty summary")
	}
	log.Infof("设备 %s 后台结果已压缩: %d -> %d 字", c.DeviceID, len([]rune(text)), len([]rune(out)))
	return out, nil
}
