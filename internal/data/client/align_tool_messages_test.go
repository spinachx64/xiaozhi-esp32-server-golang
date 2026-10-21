package client

import (
	"testing"

	"github.com/cloudwego/eino/schema"
)

func toolCall(id string) schema.ToolCall {
	return schema.ToolCall{ID: id, Type: "function", Function: schema.FunctionCall{Name: "f" + id}}
}

// Parallel tool calls must keep their assistant message once, not once per call.
func TestAlignToolMessagesKeepsParallelCallsOnce(t *testing.T) {
	msgs := []*schema.Message{
		schema.UserMessage("hi"),
		schema.AssistantMessage("", []schema.ToolCall{toolCall("a"), toolCall("b")}),
		schema.ToolMessage("ra", "a"),
		schema.ToolMessage("rb", "b"),
	}
	got := AlignToolMessages(msgs)
	if len(got) != 4 {
		t.Fatalf("len = %d, want 4", len(got))
	}
	if got[1].Role != schema.Assistant || len(got[1].ToolCalls) != 2 {
		t.Fatalf("assistant message changed: %+v", got[1])
	}
}

// Calls without a tool reply are dropped from the assistant message.
func TestAlignToolMessagesTrimsUnansweredCalls(t *testing.T) {
	msgs := []*schema.Message{
		schema.AssistantMessage("", []schema.ToolCall{toolCall("a"), toolCall("b")}),
		schema.ToolMessage("ra", "a"),
	}
	got := AlignToolMessages(msgs)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if len(got[0].ToolCalls) != 1 || got[0].ToolCalls[0].ID != "a" {
		t.Fatalf("tool calls = %+v, want only a", got[0].ToolCalls)
	}
	if len(msgs[0].ToolCalls) != 2 {
		t.Fatal("input message must not be modified")
	}
}

// An assistant message with no answered calls is dropped, unless it has text.
func TestAlignToolMessagesDropsFullyUnansweredCalls(t *testing.T) {
	msgs := []*schema.Message{
		schema.AssistantMessage("", []schema.ToolCall{toolCall("a")}),
		schema.AssistantMessage("好的", []schema.ToolCall{toolCall("b")}),
	}
	got := AlignToolMessages(msgs)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Content != "好的" || got[0].ToolCalls != nil {
		t.Fatalf("got %+v, want text-only message", got[0])
	}
}
