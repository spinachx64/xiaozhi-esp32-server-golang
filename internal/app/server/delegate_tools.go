package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"xiaozhi-esp32-server-golang/internal/app/server/chat"
	"xiaozhi-esp32-server-golang/internal/domain/delegate"
	log "xiaozhi-esp32-server-golang/logger"
)

// Local MCP tools that let the LLM hand long tasks to the agent bridge during
// a conversation. The descriptions decide the routing, so they spell out what
// belongs to the background agent and what the assistant answers itself.
const (
	delegateTaskToolName = "delegate_task"
	taskStatusToolName   = "task_status"
	cancelTaskToolName   = "cancel_task"

	delegateTaskToolDesc = "把需要较长时间才能完成的任务交给后台助手执行。后台助手可以联网查资料、读写文件、写代码、执行多个步骤。" +
		"适用于：查资料并整理总结、写代码或文档、调研对比、需要多个步骤才能完成的事情，以及用户明确说“交给后台”“帮我去做”的事。" +
		"不适用于：闲聊、常识问答、简单计算、讲笑话，以及音量、亮度、音乐等设备控制（用对应工具）。" +
		"任务在后台运行，通常需要几分钟，完成后会自动播报结果。调用成功后只需一句话告诉用户已经开始处理，不要编造结果。"
	taskStatusToolDesc = "当用户询问之前交给后台的任务做到哪了、做完没有时使用，返回进行中和最近完成的任务"
	cancelTaskToolDesc = "当用户要求取消、停止或不要再做后台任务时使用；不确定是哪个任务时不填 task_id，取消全部进行中的任务"
)

type delegateTaskParams struct {
	Task    string `json:"task" description:"交给后台助手的完整任务描述，写清楚要做什么、要什么样的结果，用户提到的关键信息都要带上" required:"true"`
	Context string `json:"context,omitempty" description:"可选：对话中与任务有关的背景信息"`
}

type cancelTaskParams struct {
	TaskID string `json:"task_id,omitempty" description:"可选：要取消的任务编号，不填则取消全部进行中的任务"`
}

// registerTools adds the delegate tools to the local MCP manager. It is a
// no-op when delegate is disabled.
func (d *delegateDelivery) registerTools() {
	if d == nil {
		return
	}
	_ = chat.RegisterLocalMcpFunc(delegateTaskToolName, delegateTaskToolDesc, delegateTaskParams{}, d.delegateTaskTool)
	_ = chat.RegisterLocalMcpFunc(taskStatusToolName, taskStatusToolDesc, struct{}{}, d.taskStatusTool)
	_ = chat.RegisterLocalMcpFunc(cancelTaskToolName, cancelTaskToolDesc, cancelTaskParams{}, d.cancelTaskTool)
	_ = chat.RegisterLocalMcpFunc(playTaskResultToolName, playTaskResultToolDesc, playTaskResultParams{}, d.playTaskResultTool)
}

const (
	playTaskResultToolName = "play_task_result"
	playTaskResultToolDesc = "当用户想现在听后台任务的结果时使用，例如回答“要”“现在听”“播放后台结果”。" +
		"结果由系统直接念给用户，调用后不要自己复述结果内容；念完后对话可以继续。"
)

type playTaskResultParams struct {
	TaskID string `json:"task_id,omitempty" description:"可选：要播放的任务编号，不填则播放最早完成的那个"`
}

// playTaskResultTool speaks a finished result inside the current
// conversation. The result is acknowledged only when it played to the end;
// otherwise it goes back to the queue and is announced once the device idles.
func (d *delegateDelivery) playTaskResultTool(ctx context.Context, args string) (string, error) {
	deviceID := chat.DeviceIDFromToolContext(ctx)
	if deviceID == "" {
		return toolError(playTaskResultToolName, "无法确定当前设备", "NO_DEVICE", "请稍后重试")
	}
	var p playTaskResultParams
	if strings.TrimSpace(args) != "" {
		_ = json.Unmarshal([]byte(args), &p)
	}
	t, ok := d.lookup(deviceID)
	if !ok || t == nil {
		return toolError(playTaskResultToolName, "设备不在线", "OFFLINE", "请稍后重试")
	}
	speaker, ok := t.(conversationSpeaker)
	if !ok {
		return toolError(playTaskResultToolName, "当前无法在对话中播放", "UNSUPPORTED", "告诉用户结果稍后会自动播报")
	}
	r, ok := d.mgr.TakeOne(deviceID, strings.TrimSpace(p.TaskID))
	if !ok {
		return chat.NewContentResponse(playTaskResultToolName, map[string]int{"pending": 0},
			"没有待播放的后台结果，告诉用户目前没有完成的任务。").ToJSON()
	}
	chat.SetTaskHint(deviceID, "")

	// Speak inside this tool call, so it is part of the current turn and the
	// turn's tts stop follows the result. The action response then ends the
	// turn without another LLM reply; the text goes to the history so the
	// user can ask follow-up questions.
	text := d.speechText(t, r)
	played, err := speaker.SpeakInTurn(ctx, text)
	if err != nil || !played {
		if err != nil {
			log.Warnf("[delegate] device=%s task=%s in-conversation playback failed: %v", deviceID, r.TaskID, err)
		} else {
			log.Infof("[delegate] device=%s task=%s in-conversation playback interrupted, requeued", deviceID, r.TaskID)
		}
		d.mgr.Requeue(deviceID, r)
		d.kick(deviceID)
		return chat.NewActionResponse(playTaskResultToolName, "play_task_result",
			"后台任务结果没有播放完，稍后会自动再播报。", "interrupted", true).ToJSON()
	}
	log.Infof("[delegate] device=%s task=%s played in conversation", deviceID, r.TaskID)
	d.mgr.Delivered(deviceID, r.TaskID)
	d.forget(r.TaskID)
	return chat.NewActionResponse(playTaskResultToolName, "play_task_result",
		"已把后台任务结果念给用户："+text, "played", true).ToJSON()
}

func toolError(tool, msg, code, hint string) (string, error) {
	return chat.NewErrorResponse(tool, msg, code, hint).ToJSON()
}

func (d *delegateDelivery) delegateTaskTool(ctx context.Context, args string) (string, error) {
	deviceID := chat.DeviceIDFromToolContext(ctx)
	if deviceID == "" {
		return toolError(delegateTaskToolName, "无法确定当前设备", "NO_DEVICE", "请稍后重试")
	}
	var p delegateTaskParams
	if err := json.Unmarshal([]byte(args), &p); err != nil || strings.TrimSpace(p.Task) == "" {
		return toolError(delegateTaskToolName, "缺少任务描述", "INVALID_TASK", "请把要做的事情写进 task 参数")
	}
	id, err := d.mgr.Submit(deviceID, p.Task, p.Context)
	if errors.Is(err, delegate.ErrTooManyTasks) {
		return toolError(delegateTaskToolName, "后台已有任务在执行", "TOO_MANY_TASKS", "告诉用户等当前任务完成后再提交，或者先取消它")
	}
	if err != nil {
		return toolError(delegateTaskToolName, err.Error(), "SUBMIT_FAILED", "告诉用户后台暂时不可用")
	}
	log.Infof("[delegate] device=%s task=%s submitted by LLM: %s", deviceID, id, p.Task)
	return chat.NewContentResponse(delegateTaskToolName,
		map[string]string{"task_id": id, "status": "running"},
		"任务已交给后台，完成后会自动播报结果。用一句话告诉用户已经开始处理即可。").ToJSON()
}

func (d *delegateDelivery) taskStatusTool(ctx context.Context, _ string) (string, error) {
	deviceID := chat.DeviceIDFromToolContext(ctx)
	if deviceID == "" {
		return toolError(taskStatusToolName, "无法确定当前设备", "NO_DEVICE", "请稍后重试")
	}
	running, recent := d.mgr.Status(deviceID)
	type runningOut struct {
		TaskID   string `json:"task_id"`
		Task     string `json:"task"`
		Minutes  int    `json:"running_minutes"`
		Progress string `json:"progress,omitempty"`
	}
	type recentOut struct {
		TaskID string `json:"task_id"`
		Task   string `json:"task"`
		OK     bool   `json:"ok"`
	}
	data := struct {
		Running []runningOut `json:"running"`
		Recent  []recentOut  `json:"recent"`
	}{Running: []runningOut{}, Recent: []recentOut{}}
	for _, t := range running {
		data.Running = append(data.Running, runningOut{t.ID, t.Task, int(time.Since(t.Started).Minutes()), t.Progress})
	}
	for _, r := range recent {
		data.Recent = append(data.Recent, recentOut{r.TaskID, r.Task, r.Err == nil})
	}
	msg := fmt.Sprintf("进行中 %d 个，最近完成 %d 个。完成的结果会自动播报，不要复述结果原文。", len(data.Running), len(data.Recent))
	return chat.NewContentResponse(taskStatusToolName, data, msg).ToJSON()
}

func (d *delegateDelivery) cancelTaskTool(ctx context.Context, args string) (string, error) {
	deviceID := chat.DeviceIDFromToolContext(ctx)
	if deviceID == "" {
		return toolError(cancelTaskToolName, "无法确定当前设备", "NO_DEVICE", "请稍后重试")
	}
	var p cancelTaskParams
	if strings.TrimSpace(args) != "" {
		_ = json.Unmarshal([]byte(args), &p)
	}
	n := d.mgr.Cancel(deviceID, strings.TrimSpace(p.TaskID))
	msg := fmt.Sprintf("已取消 %d 个任务。", n)
	if n == 0 {
		msg = "没有可以取消的进行中任务。"
	}
	return chat.NewContentResponse(cancelTaskToolName, map[string]int{"canceled": n}, msg).ToJSON()
}
