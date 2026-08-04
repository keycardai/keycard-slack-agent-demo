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

func TestResolvedToolsAreDescribedForThePrompt(t *testing.T) {
	// The prompt's tool list is built from these specs, and it used to carry only the static tools while
	// the API carried 64. The prompt won: the model reported it could not reach Linear and asked for an
	// authorization link instead of calling the tool it had been given.
	specs := describeResolvedTools(
		map[string]MCPToolRef{
			"linear_list_issues": {Server: MCPServer{Name: "Linear MCP"}},
		},
		map[string]AuthToolRef{
			"authorize_payroll": {Resource: "https://mcp.payroll.example.com/mcp", Label: "Payroll MCP"},
		},
	)
	if len(specs) != 2 {
		t.Fatalf("expected both tools described, got %+v", specs)
	}
	// Sorted, so the prompt is stable between runs rather than reordering with map iteration.
	if specs[0].Name != "authorize_payroll" || specs[1].Name != "linear_list_issues" {
		t.Fatalf("expected a stable sorted order, got %+v", specs)
	}
	if !strings.Contains(specs[1].Description, "Linear MCP") {
		t.Fatalf("an MCP tool should name its server: %+v", specs[1])
	}
	if !strings.Contains(specs[0].Description, "Payroll MCP") {
		t.Fatalf("an authorize tool should name what it authorizes: %+v", specs[0])
	}
}

func TestNoResolvedToolsDescribesNothing(t *testing.T) {
	if specs := describeResolvedTools(nil, nil); len(specs) != 0 {
		t.Fatalf("expected no specs, got %+v", specs)
	}
}
