//go:build golden

package agent

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/keycardai/keycard-slack-agent-demo/app"
	"github.com/keycardai/keycard-slack-agent-demo/crypto"
	"github.com/keycardai/keycard-slack-agent-demo/db"
	"github.com/keycardai/keycard-slack-agent-demo/prompts"
)

// goldenScenario is the JSON schema for a golden file test case.
type goldenScenario struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Skip        string       `json:"skip,omitempty"`
	NeedsLLM    bool         `json:"needs_llm"`
	Context     goldenCtx    `json:"context"`
	UserMessage string       `json:"user_message"`
	Setup       goldenSetup  `json:"setup"`
	Expect      goldenExpect `json:"expect"`
}

type goldenCtx struct {
	KeycardIdentity string `json:"keycard_identity"`
	TeamID          string `json:"team_id"`
	UserID          string `json:"user_id"`
	ChannelID       string `json:"channel_id"`
	ThreadTS        string `json:"thread_ts"`
	MessageTS       string `json:"message_ts"`
	ChatType        string `json:"chat_type"`
	IsPrivate       bool   `json:"is_private"`
}

type goldenSetup struct {
	KeycardToken string `json:"keycard_token"`
}

type goldenExpect struct {
	Completed        *bool    `json:"completed,omitempty"`
	NoErrors         *bool    `json:"no_errors,omitempty"`
	MaxTurns         int      `json:"max_turns,omitempty"`
	ToolsCalled      []string `json:"tools_called,omitempty"`
	ToolsNotCalled   []string `json:"tools_not_called,omitempty"`
	TextContains     []string `json:"text_contains,omitempty"`
	TextContainsAny  []string `json:"text_contains_any,omitempty"`
	TextNotContains  []string `json:"text_not_contains,omitempty"`
	HasToolCallCount *int     `json:"has_tool_call_count,omitempty"`
}

// goldenResult captures the output of a single golden test run.
type goldenResult struct {
	events     []AgentEvent
	text       string
	toolCalls  []string
	toolEvents []AgentEvent
	errors     []string
	completed  bool
	turns      int
}

func TestGolden(t *testing.T) {
	scenarios := loadGoldenScenarios(t)
	if len(scenarios) == 0 {
		t.Skip("no golden scenarios found")
	}

	for _, scenario := range scenarios {
		t.Run(scenario.Name, func(t *testing.T) {
			if scenario.Skip != "" {
				t.Skip(scenario.Skip)
			}
			if scenario.NeedsLLM && os.Getenv("ANTHROPIC_API_KEY") == "" {
				t.Skip("ANTHROPIC_API_KEY not set, skipping LLM scenario")
			}
			runGoldenScenario(t, scenario)
		})
	}
}

func loadGoldenScenarios(t *testing.T) []goldenScenario {
	t.Helper()
	pattern := filepath.Join("testdata", "golden", "*.json")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob golden files: %v", err)
	}
	scenarios := make([]goldenScenario, 0, len(matches))
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var s goldenScenario
		if err := json.Unmarshal(data, &s); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		if s.Name == "" {
			s.Name = strings.TrimSuffix(filepath.Base(path), ".json")
		}
		scenarios = append(scenarios, s)
	}
	return scenarios
}

func runGoldenScenario(t *testing.T, s goldenScenario) {
	t.Helper()

	// db.Open reads schema.sql from cwd; chdir to the db package directory.

	// Set up temp database.
	dbPath := filepath.Join(t.TempDir(), "test.db")
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate key: %v", err)
	}
	cipher, err := crypto.NewCipher(key)
	if err != nil {
		t.Fatalf("create cipher: %v", err)
	}
	database, err := db.Open(dbPath, cipher)
	if err != nil {
		if strings.Contains(err.Error(), "fts5") {
			t.Skip("fts5 not available in sqlite build")
		}
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	// Build runner.
	var client anthropic.Client
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		client = anthropic.NewClient()
	}

	runner := NewRunner(
		database,
		&client,
		prompts.System,
	)

	// Fake slack actions (no-op).
	runner.SlackActions = &fakeSlackActions{}

	// Build request context.
	reqCtx := &app.RequestContext{
		KeycardIdentity: s.Context.KeycardIdentity,
		TeamID:          s.Context.TeamID,
		UserID:          s.Context.UserID,
		ChannelID:       s.Context.ChannelID,
		ThreadTS:        s.Context.ThreadTS,
		MessageTS:       s.Context.MessageTS,
		SessionID:       app.MakeSessionID(s.Context.TeamID, s.Context.ChannelID, s.Context.ThreadTS),
		ChatType:        s.Context.ChatType,
		IsPrivate:       s.Context.IsPrivate,
	}

	// Ensure session exists in DB.
	_, _ = database.GetOrCreateSession(reqCtx.SessionID, reqCtx.TeamID, reqCtx.ChannelID, reqCtx.ThreadTS)

	// Run the agent.
	keycardToken := s.Setup.KeycardToken
	if keycardToken == "" {
		keycardToken = "test-keycard-token"
	}

	timeout := 90 * time.Second
	if s.Expect.MaxTurns > 0 && s.Expect.MaxTurns <= 3 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	events := make(chan AgentEvent, 256)
	go runner.Run(ctx, reqCtx, keycardToken, s.UserMessage, events)

	result := collectGoldenResult(events)

	// Run assertions.
	assertGoldenExpectations(t, s.Expect, result)
}

func collectGoldenResult(events <-chan AgentEvent) goldenResult {
	var r goldenResult
	var text strings.Builder
	for event := range events {
		r.events = append(r.events, event)
		switch event.Type {
		case EventText:
			text.WriteString(event.Content)
		case EventToolCall:
			r.toolCalls = append(r.toolCalls, event.ToolName)
			r.toolEvents = append(r.toolEvents, event)
		case EventError:
			r.errors = append(r.errors, event.Content)
		case EventDone:
			r.completed = true
		case EventTurnStart:
			r.turns++
		}
	}
	r.text = text.String()
	return r
}

func assertGoldenExpectations(t *testing.T, expect goldenExpect, result goldenResult) {
	t.Helper()

	// Check completion.
	if expect.Completed != nil && *expect.Completed != result.completed {
		t.Errorf("expected completed=%v, got %v", *expect.Completed, result.completed)
	}

	// Check errors.
	if expect.NoErrors != nil && *expect.NoErrors && len(result.errors) > 0 {
		t.Errorf("expected no errors, got %d: %v", len(result.errors), result.errors)
	}

	// Check max turns.
	if expect.MaxTurns > 0 && result.turns > expect.MaxTurns {
		t.Errorf("expected at most %d turns, got %d", expect.MaxTurns, result.turns)
	}

	// Check tools called (unordered set — each expected tool must appear at least once).
	if len(expect.ToolsCalled) > 0 {
		toolSet := make(map[string]bool)
		for _, name := range result.toolCalls {
			toolSet[name] = true
		}
		for _, expected := range expect.ToolsCalled {
			if !toolSet[expected] {
				t.Errorf("expected tool %q to be called, got events: %v", expected, result.toolCalls)
			}
		}
	}

	// Check tools NOT called.
	if len(expect.ToolsNotCalled) > 0 {
		toolSet := make(map[string]bool)
		for _, name := range result.toolCalls {
			toolSet[name] = true
		}
		for _, notExpected := range expect.ToolsNotCalled {
			if toolSet[notExpected] {
				t.Errorf("expected tool %q NOT to be called, but it was", notExpected)
			}
		}
	}

	// Check tool call count.
	if expect.HasToolCallCount != nil {
		if len(result.toolCalls) != *expect.HasToolCallCount {
			t.Errorf("expected %d tool calls, got %d: %v", *expect.HasToolCallCount, len(result.toolCalls), result.toolCalls)
		}
	}

	// Check text contains (all must match).
	if len(expect.TextContains) > 0 {
		lower := strings.ToLower(result.text)
		for _, needle := range expect.TextContains {
			if !strings.Contains(lower, strings.ToLower(needle)) {
				t.Errorf("expected response to contain %q, got: %s", needle, truncate(result.text, 500))
			}
		}
	}

	// Check text contains any (at least one must match).
	if len(expect.TextContainsAny) > 0 {
		lower := strings.ToLower(result.text)
		found := false
		for _, needle := range expect.TextContainsAny {
			if strings.Contains(lower, strings.ToLower(needle)) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected response to contain at least one of %v, got: %s", expect.TextContainsAny, truncate(result.text, 500))
		}
	}

	// Check text not contains.
	if len(expect.TextNotContains) > 0 {
		lower := strings.ToLower(result.text)
		for _, needle := range expect.TextNotContains {
			if strings.Contains(lower, strings.ToLower(needle)) {
				t.Errorf("expected response NOT to contain %q, but it does", needle)
			}
		}
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// fakeSlackActions is a no-op implementation of SlackActioner for golden tests.
type fakeSlackActions struct {
	messages []fakeSlackMessage
}

type fakeSlackMessage struct {
	Channel  string
	Text     string
	ThreadTS string
}

func (f *fakeSlackActions) OpenDM(_ context.Context, userID string) (string, error) {
	return "D-fake-" + userID, nil
}

func (f *fakeSlackActions) PostMessage(channel, text, threadTS string) (string, error) {
	f.messages = append(f.messages, fakeSlackMessage{
		Channel:  channel,
		Text:     text,
		ThreadTS: threadTS,
	})
	return "fake-ts", nil
}

func (f *fakeSlackActions) UpdateMessage(_, _, _ string) error { return nil }
func (f *fakeSlackActions) DeleteMessage(_, _ string) error    { return nil }
func (f *fakeSlackActions) AddReaction(_, _, _ string) error   { return nil }
