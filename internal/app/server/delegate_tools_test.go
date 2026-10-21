package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"xiaozhi-esp32-server-golang/internal/app/server/chat"
)

func toolCtx(deviceID string) context.Context {
	//nolint:staticcheck // the chat package keys the operator with this string
	return context.WithValue(context.Background(), "chat_session_operator", &chat.ChatManager{DeviceID: deviceID})
}

func TestDelegateTaskToolSubmitsForCurrentDevice(t *testing.T) {
	bridge := &fakeBridge{release: make(chan struct{})}
	d, _, mgr := setup(t, bridge)
	defer close(bridge.release)

	out, err := d.delegateTaskTool(toolCtx(dev), `{"task":"查一下明天的天气并总结"}`)
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Success bool              `json:"success"`
		Data    map[string]string `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil || !resp.Success || resp.Data["task_id"] == "" {
		t.Fatalf("unexpected response %s (err %v)", out, err)
	}
	running, _ := mgr.Status(dev)
	if len(running) != 1 || running[0].Task != "查一下明天的天气并总结" {
		t.Fatalf("running = %+v", running)
	}

	status, _ := d.taskStatusTool(toolCtx(dev), "")
	if !strings.Contains(status, resp.Data["task_id"]) {
		t.Fatalf("status does not list the task: %s", status)
	}

	cancel, _ := d.cancelTaskTool(toolCtx(dev), "")
	if !strings.Contains(cancel, `"canceled":1`) {
		t.Fatalf("cancel = %s", cancel)
	}
}

func TestDelegateTaskToolRejectsMissingInput(t *testing.T) {
	d, _, _ := setup(t, &fakeBridge{})
	if out, _ := d.delegateTaskTool(context.Background(), `{"task":"x"}`); !strings.Contains(out, "NO_DEVICE") {
		t.Fatalf("no device: %s", out)
	}
	if out, _ := d.delegateTaskTool(toolCtx(dev), `{"task":"  "}`); !strings.Contains(out, "INVALID_TASK") {
		t.Fatalf("empty task: %s", out)
	}
}
