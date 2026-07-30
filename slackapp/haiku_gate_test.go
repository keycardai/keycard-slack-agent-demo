//go:build golden

package slackapp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

type haikuGateScenario struct {
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	ThreadContext []threadMessage `json:"thread_context"`
	NewMessage    string          `json:"new_message"`
	ExpectRespond bool            `json:"expect_respond"`
}

type threadMessage struct {
	Author string `json:"author"`
	Text   string `json:"text"`
}

func TestGoldenHaikuGate(t *testing.T) {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY not set, skipping Haiku gate golden tests")
	}

	scenarios := loadHaikuGateScenarios(t)
	if len(scenarios) == 0 {
		t.Fatal("no haiku gate golden scenarios found")
	}

	client := anthropic.NewClient()

	for _, scenario := range scenarios {
		t.Run(scenario.Name, func(t *testing.T) {
			// Build thread context in the same format as fetchThreadContext.
			var sb strings.Builder
			sb.WriteString("[thread context]\n")
			for _, msg := range scenario.ThreadContext {
				sb.WriteString(msg.Author + ": " + msg.Text + "\n")
			}
			sb.WriteString("[end thread context]")

			prompt := sb.String() + "\n\nNew message: " + scenario.NewMessage

			resp := callHaikuWithRetry(t, &client, prompt, 5)

			var answer string
			for _, block := range resp.Content {
				if block.Type == "text" {
					answer = strings.TrimSpace(strings.ToLower(block.Text))
					break
				}
			}

			responded := strings.HasPrefix(answer, "yes")
			if responded != scenario.ExpectRespond {
				t.Errorf("expected respond=%v, got answer=%q", scenario.ExpectRespond, answer)
			} else {
				t.Logf("ok: answer=%q expect_respond=%v", answer, scenario.ExpectRespond)
			}
		})
	}
}

func callHaikuWithRetry(t *testing.T, client *anthropic.Client, prompt string, maxAttempts int) *anthropic.Message {
	t.Helper()
	for attempt := range maxAttempts {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		resp, err := client.Messages.New(ctx, anthropic.MessageNewParams{
			Model:     anthropic.Model("claude-haiku-4-5-20251001"),
			MaxTokens: 4,
			System:    []anthropic.TextBlockParam{{Text: haikuGatePrompt}},
			Messages: []anthropic.MessageParam{
				{
					Role:    anthropic.MessageParamRoleUser,
					Content: []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock(prompt)},
				},
			},
		})
		cancel()

		if err == nil {
			return resp
		}

		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) && (apiErr.StatusCode == 429 || apiErr.StatusCode == 529) {
			delay := time.Duration(2<<attempt) * time.Second
			t.Logf("retry %d/%d after %v: %v", attempt+1, maxAttempts, delay, err)
			time.Sleep(delay)
			continue
		}
		t.Fatalf("haiku call failed: %v", err)
	}
	t.Fatal("haiku call: max retries exceeded")
	return nil
}

func loadHaikuGateScenarios(t *testing.T) []haikuGateScenario {
	t.Helper()
	pattern := filepath.Join("testdata", "golden", "haiku_gate_*.json")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob haiku gate files: %v", err)
	}
	scenarios := make([]haikuGateScenario, 0, len(matches))
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var s haikuGateScenario
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
