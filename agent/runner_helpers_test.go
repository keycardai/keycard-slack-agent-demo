package agent

import (
	"strings"
	"testing"
)

func TestBuildConsentURL(t *testing.T) {
	got := buildConsentURL("https://example.com", "T1", "U1", "C1", "https://api.github.com")
	if got == "" {
		t.Fatal("expected non-empty URL")
	}
	for _, sub := range []string{"team=T1", "user=U1", "channel=C1", "resource="} {
		if !strings.Contains(got, sub) {
			t.Errorf("expected URL to contain %q, got %q", sub, got)
		}
	}
}

func TestBuildConsentURLMultipleResources(t *testing.T) {
	got := buildConsentURL("https://example.com", "T1", "U1", "C1", "https://api.github.com", "https://api.linear.app")
	if got == "" {
		t.Fatal("expected non-empty URL")
	}
	if strings.Count(got, "resource=") != 2 {
		t.Fatalf("expected 2 resource params, got %q", got)
	}
}

func TestBuildConsentURLEmptyBase(t *testing.T) {
	got := buildConsentURL("", "T1", "U1", "C1", "res")
	if got != "" {
		t.Fatalf("expected empty for empty base URL, got %q", got)
	}
}

func TestBuildConsentURLTrailingSlash(t *testing.T) {
	got := buildConsentURL("https://example.com/", "T1", "U1", "C1", "res")
	if got == "" {
		t.Fatal("expected non-empty URL")
	}
	if strings.Contains(got, "//oauth") {
		t.Fatalf("expected no double slashes, got %q", got)
	}
}

func TestBuildConsentURLNoResource(t *testing.T) {
	got := buildConsentURL("https://example.com", "T1", "U1", "C1")
	if got == "" {
		t.Fatal("expected non-empty URL")
	}
	if strings.Contains(got, "resource=") {
		t.Fatalf("expected no resource param when empty, got %q", got)
	}
}

func TestParseDatadogCredentialPairValid(t *testing.T) {
	input := `{"api_key":"ak123","app_key":"ap456"}`
	api, app := parseDatadogCredentialPair(input)
	if api != "ak123" {
		t.Fatalf("expected api_key 'ak123', got %q", api)
	}
	if app != "ap456" {
		t.Fatalf("expected app_key 'ap456', got %q", app)
	}
}

func TestParseDatadogCredentialPairEmpty(t *testing.T) {
	api, app := parseDatadogCredentialPair("")
	if api != "" || app != "" {
		t.Fatalf("expected empty, got %q/%q", api, app)
	}
}

func TestParseDatadogCredentialPairNotJSON(t *testing.T) {
	api, app := parseDatadogCredentialPair("plain-token")
	if api != "" || app != "" {
		t.Fatalf("expected empty for non-JSON, got %q/%q", api, app)
	}
}

func TestParseDatadogCredentialPairMissingField(t *testing.T) {
	api, app := parseDatadogCredentialPair(`{"api_key":"ak123"}`)
	if api != "" || app != "" {
		t.Fatalf("expected empty when app_key missing, got %q/%q", api, app)
	}
}

func TestCoerceInt(t *testing.T) {
	cases := []struct {
		input    any
		expected int
	}{
		{float64(42), 42},
		{float32(7), 7},
		{int(3), 3},
		{int64(99), 99},
		{"10", 10},
		{"bad", 0},
		{nil, 0},
	}
	for _, tc := range cases {
		got := coerceInt(tc.input)
		if got != tc.expected {
			t.Errorf("coerceInt(%v) = %d, want %d", tc.input, got, tc.expected)
		}
	}
}

func TestCoerceInt64(t *testing.T) {
	cases := []struct {
		input    any
		expected int64
	}{
		{float64(42), 42},
		{int(3), 3},
		{int64(99), 99},
		{"10", 10},
		{"bad", 0},
		{nil, 0},
	}
	for _, tc := range cases {
		got := coerceInt64(tc.input)
		if got != tc.expected {
			t.Errorf("coerceInt64(%v) = %d, want %d", tc.input, got, tc.expected)
		}
	}
}

func TestIsSandboxTool(t *testing.T) {
	for _, name := range []string{"slack_action", "authorize_resource"} {
		if isSandboxTool(name) {
			t.Errorf("expected %q to NOT be a sandbox tool", name)
		}
	}
}

func TestToolSpecsForRequestReturnsAllTools(t *testing.T) {
	specs := ToolList()
	r := &Runner{ToolSpecs: specs}
	got := r.toolSpecsForRequest(nil)
	if len(got) != len(specs) {
		t.Fatalf("expected %d specs, got %d", len(specs), len(got))
	}
}

func TestTruncateLogValue(t *testing.T) {
	if got := truncateLogValue("short", 100); got != "short" {
		t.Fatalf("expected 'short', got %q", got)
	}
	if got := truncateLogValue("abcdef", 3); got != "abc..." {
		t.Fatalf("expected 'abc...', got %q", got)
	}
	if got := truncateLogValue("  padded  ", 100); got != "padded" {
		t.Fatalf("expected trimmed, got %q", got)
	}
}
