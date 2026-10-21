package chat

import "context"

// DeviceIDFromToolContext returns the device of the conversation a local MCP
// tool is running in, or "" when the context carries no ChatManager.
func DeviceIDFromToolContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if cm, ok := ctx.Value("chat_session_operator").(*ChatManager); ok && cm != nil {
		return cm.DeviceID
	}
	return ""
}
