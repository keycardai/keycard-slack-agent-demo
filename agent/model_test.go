package agent

import "testing"

func TestRunnerFallsBackToTheDefaultModel(t *testing.T) {
	// The model used to be pinned in the API call, which is how it ended up a generation behind
	// with no way to change it short of editing the source.
	runner := &Runner{}
	if got := runner.model(); got != DefaultModel {
		t.Fatalf("expected %q, got %q", DefaultModel, got)
	}
}

func TestRunnerHonoursAConfiguredModel(t *testing.T) {
	runner := &Runner{Model: "claude-sonnet-5"}
	if got := runner.model(); got != "claude-sonnet-5" {
		t.Fatalf("expected the configured model, got %q", got)
	}
}

func TestRunnerIgnoresABlankModel(t *testing.T) {
	runner := &Runner{Model: "   "}
	if got := runner.model(); got != DefaultModel {
		t.Fatalf("expected whitespace to fall back to the default, got %q", got)
	}
}
