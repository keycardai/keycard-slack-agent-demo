package slackapp

import (
	"context"
	"testing"
	"time"
)

func testHandler(overrides *Handler) *Handler {
	h := &Handler{
		handledKeys: make(map[string]time.Time),
	}
	if overrides != nil {
		if overrides.Slack != nil {
			h.Slack = overrides.Slack
		}
		if overrides.botUserID != "" {
			h.botUserID = overrides.botUserID
		}
		if overrides.runAgentFunc != nil {
			h.runAgentFunc = overrides.runAgentFunc
		}
	}
	return h
}

func TestShouldHandleMessage_DirectMessageAlwaysHandled(t *testing.T) {
	// DM traffic is unconditional: if a user sends a direct message, we
	// should enter the agent loop without mention/thread gating checks.
	h := testHandler(nil)
	if !h.shouldHandleMessage(context.Background(), "D123", "im", "hello", "", "111.222") {
		t.Fatal("expected DM to be handled")
	}
}

func TestShouldHandleMessage_PublicRootRequiresMention(t *testing.T) {
	// Root channel messages are conservative by default: skip unless there is
	// an explicit bot mention in the message text.
	h := testHandler(&Handler{botUserID: "B123"})
	if h.shouldHandleMessage(context.Background(), "C123", "channel", "hello", "", "111.222") {
		t.Fatal("expected unmentioned public root message to be skipped")
	}
	if !h.shouldHandleMessage(context.Background(), "C123", "channel", "<@B123> hello", "", "111.222") {
		t.Fatal("expected mentioned public root message to be handled")
	}
}

func TestShouldHandleMessage_PublicThreadRequiresMention(t *testing.T) {
	// Every channel message (root or thread) requires an explicit @-mention.
	h := testHandler(&Handler{botUserID: "B123"})
	if !h.shouldHandleMessage(context.Background(), "C123", "channel", "<@B123> in-thread", "111.222", "111.333") {
		t.Fatal("expected mentioned thread message to be handled")
	}
	if h.shouldHandleMessage(context.Background(), "C123", "channel", "follow-up, no mention", "111.222", "111.334") {
		t.Fatal("expected unmentioned thread message to be skipped")
	}
}
