package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

func TestHandleAssistantResponseExtractsToolCalls(t *testing.T) {
	resp := &anthropic.Message{
		Content: []anthropic.ContentBlockUnion{
			{Type: "text", Text: "hi"},
			{
				Type:  "tool_use",
				ID:    "tool-1",
				Name:  "slack_action",
				Input: json.RawMessage(`{"action":"react"}`),
			},
			{
				Type:  "tool_use",
				ID:    "tool-2",
				Name:  "authorize_resource",
				Input: json.RawMessage(`{"resource":"https://api.github.com"}`),
			},
		},
	}

	var assistantText strings.Builder
	events := make(chan AgentEvent, 10)

	toolCalls := handleAssistantResponse(resp, &assistantText, events)

	if assistantText.String() != "hi" {
		t.Fatalf("expected assistant text to be hi, got %q", assistantText.String())
	}
	if len(toolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(toolCalls))
	}
	if toolCalls[0].Name != "slack_action" {
		t.Fatalf("expected first tool to be slack_action, got %q", toolCalls[0].Name)
	}
	if toolCalls[0].Input["action"] != "react" {
		t.Fatalf("expected action react, got %v", toolCalls[0].Input["action"])
	}
	if toolCalls[1].Name != "authorize_resource" {
		t.Fatalf("expected second tool to be authorize_resource, got %q", toolCalls[1].Name)
	}
	if toolCalls[1].Input["resource"] != "https://api.github.com" {
		t.Fatalf("expected github resource, got %v", toolCalls[1].Input["resource"])
	}
}

func TestIsErrorOutput(t *testing.T) {
	if !isErrorOutput("Error: boom") {
		t.Fatal("expected error output to be detected")
	}
	if !isErrorOutput("error reading file") {
		t.Fatal("expected lowercase error output to be detected")
	}
	if isErrorOutput("ok") {
		t.Fatal("expected ok to not be treated as error")
	}
}
