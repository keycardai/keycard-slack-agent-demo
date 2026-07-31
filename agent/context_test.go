package agent

import (
	"strings"
	"testing"

	"github.com/keycardai/keycard-slack-agent-demo/app"
)

func TestRenderTemplate(t *testing.T) {
	tmpl := "Hello {{NAME}}, you are in {{PLACE}}."
	values := map[string]string{
		"NAME":  "Alice",
		"PLACE": "Wonderland",
	}
	got := renderTemplate(tmpl, values)
	expected := "Hello Alice, you are in Wonderland."
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestRenderTemplateUnusedKeys(t *testing.T) {
	tmpl := "Hello {{NAME}}."
	values := map[string]string{
		"NAME":  "Bob",
		"EXTRA": "unused",
	}
	got := renderTemplate(tmpl, values)
	if got != "Hello Bob." {
		t.Fatalf("unexpected: %q", got)
	}
}

func TestRenderTemplateMissingKey(t *testing.T) {
	tmpl := "Hello {{NAME}}, welcome to {{PLACE}}."
	values := map[string]string{
		"NAME": "Bob",
	}
	got := renderTemplate(tmpl, values)
	if !strings.Contains(got, "{{PLACE}}") {
		t.Fatalf("expected unreplaced token to remain, got %q", got)
	}
}

func TestFormatToolsEmpty(t *testing.T) {
	if got := formatTools(nil); got != "(no tools available)" {
		t.Fatalf("expected (no tools available), got %q", got)
	}
}

func TestFormatTools(t *testing.T) {
	tools := []ToolSpec{
		{Name: "slack_action", Description: "Slack action"},
		{Name: "authorize_resource", Description: "Authorize"},
	}
	got := formatTools(tools)
	if !strings.Contains(got, "slack_action") {
		t.Fatalf("expected slack_action in output, got %q", got)
	}
	if !strings.Contains(got, "authorize_resource") {
		t.Fatalf("expected authorize_resource in output, got %q", got)
	}
}

func TestBuildChatModeRules(t *testing.T) {
	public := buildChatModeRules(&app.RequestContext{ChatType: "public"})
	if strings.TrimSpace(public) == "" {
		t.Fatal("expected non-empty public mode rules")
	}

	private := buildChatModeRules(&app.RequestContext{ChatType: "private"})
	if strings.TrimSpace(private) == "" {
		t.Fatal("expected non-empty private mode rules")
	}

	if public == private {
		t.Fatal("expected different rules for public vs private")
	}
}
