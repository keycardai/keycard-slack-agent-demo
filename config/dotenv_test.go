package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeEnv(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestLoadEnvFileSetsVariables(t *testing.T) {
	// The bug this fixes: a filled-in .env did nothing, because Go has no convention for reading one,
	// so every required variable came back missing at once and the error could not say why.
	path := writeEnv(t, "SLACK_BOT_TOKEN=xoxb-from-file\nKEYCARD_ZONE_ID=zone_1\n")
	t.Setenv("SLACK_BOT_TOKEN", "")
	_ = os.Unsetenv("SLACK_BOT_TOKEN")
	_ = os.Unsetenv("KEYCARD_ZONE_ID")

	if _, err := LoadEnvFile(path); err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := os.Getenv("SLACK_BOT_TOKEN"); got != "xoxb-from-file" {
		t.Fatalf("unexpected value %q", got)
	}
	if got := os.Getenv("KEYCARD_ZONE_ID"); got != "zone_1" {
		t.Fatalf("unexpected value %q", got)
	}
}

func TestTheRealEnvironmentWins(t *testing.T) {
	// A deployment sets real environment variables. A .env that shipped in the image by accident must
	// never override them, or production quietly runs on a developer's values.
	path := writeEnv(t, "KEYCARD_ZONE_ID=from-file\n")
	t.Setenv("KEYCARD_ZONE_ID", "from-the-platform")

	if _, err := LoadEnvFile(path); err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := os.Getenv("KEYCARD_ZONE_ID"); got != "from-the-platform" {
		t.Fatalf("the file overrode the real environment: %q", got)
	}
}

func TestAMissingFileIsNotAnError(t *testing.T) {
	// Production has no .env, so its absence cannot be fatal.
	if _, err := LoadEnvFile(filepath.Join(t.TempDir(), "nope.env")); err != nil {
		t.Fatalf("expected a missing file to be fine, got %v", err)
	}
}

func TestParseEnvLine(t *testing.T) {
	cases := []struct {
		line, key, value string
		ok               bool
	}{
		{"KEY=value", "KEY", "value", true},
		{"  KEY = value  ", "KEY", "value", true},
		{"export KEY=value", "KEY", "value", true},
		{`KEY="quoted value"`, "KEY", "quoted value", true},
		{"KEY='quoted value'", "KEY", "quoted value", true},
		// A secret may legitimately contain a #, so only a bare value gets a trailing comment stripped.
		{`KEY="secret#withhash"`, "KEY", "secret#withhash", true},
		{"KEY=bare # trailing comment", "KEY", "bare", true},
		{"KEY=", "KEY", "", true},
		{"# a comment", "", "", false},
		{"", "", "", false},
		{"   ", "", "", false},
		{"NOEQUALS", "", "", false},
		{"=novalue", "", "", false},
	}
	for _, c := range cases {
		key, value, ok := parseEnvLine(c.line)
		if ok != c.ok || key != c.key || value != c.value {
			t.Errorf("parseEnvLine(%q) = (%q, %q, %v), want (%q, %q, %v)",
				c.line, key, value, ok, c.key, c.value, c.ok)
		}
	}
}

func TestLoadNamesTheFileItLookedForWhenNothingIsSet(t *testing.T) {
	// "Everything is missing" nearly always means the file was not found, so the error says so.
	for _, key := range []string{
		"SLACK_BOT_TOKEN", "SLACK_APP_TOKEN", "KEYCARD_ZONE_URL", "KEYCARD_ZONE_ID",
		"KEYCARD_CLIENT_ID", "KEYCARD_CLIENT_SECRET", "KEYCARD_RESOURCE", "BASE_URL",
		"ENCRYPTION_KEY", "STATE_SECRET",
	} {
		t.Setenv(key, "")
		_ = os.Unsetenv(key)
	}
	t.Setenv("ENV_FILE", filepath.Join(t.TempDir(), "absent.env"))

	_, err := Load()
	if err == nil {
		t.Fatal("expected the missing-variable error")
	}
	if !contains(err.Error(), "absent.env") {
		t.Fatalf("error should name the file it looked for: %v", err)
	}
	if !contains(err.Error(), ".env.example") {
		t.Fatalf("error should say what to do: %v", err)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

func TestPlaceholdersAreRejected(t *testing.T) {
	// Worse than a missing value, because they are present so nothing complains, and the failure
	// surfaces much later and somewhere unrelated.
	cases := []struct{ name, value string }{
		{"KEYCARD_SERVICE_ACCOUNT_ID", "..."},
		{"BASE_URL", "https://your-agent.example.com"},
		{"STATE_SECRET", "change-me"},
		{"ENCRYPTION_KEY", "change-me-32-bytes-exactly!!!!!!"},
		{"SLACK_BOT_TOKEN", "xoxb-..."},
		// The paste error: the value repeats its own key.
		{"DATABASE_PATH", "/DATABASE_PATH=./slack-agent.db"},
	}
	for _, c := range cases {
		if !isPlaceholder(c.name, c.value) {
			t.Errorf("expected %s=%q to be treated as a placeholder", c.name, c.value)
		}
	}
}

func TestRealValuesAreNotPlaceholders(t *testing.T) {
	// A secret can contain almost anything, so the check has to stay narrow.
	cases := []struct{ name, value string }{
		{"SLACK_BOT_TOKEN", "xoxb-9f8a7b6c5d4e"},
		{"BASE_URL", "http://localhost:8080"},
		{"BASE_URL", "https://agent.acme.com"},
		// base64 padding ends with = and must not trip the self-key check
		{"ENCRYPTION_KEY", "c2VjcmV0LWtleS12YWx1ZS0zMg=="},
		{"KEYCARD_RESOURCE", "urn:keycard:slack-agent"},
		// a connection string is full of equals signs
		{"DATABASE_PATH", "./agent.db?cache=shared&mode=rwc"},
		// empty is a separate problem, reported as missing
		{"STATE_SECRET", ""},
	}
	for _, c := range cases {
		if isPlaceholder(c.name, c.value) {
			t.Errorf("%s=%q should not be treated as a placeholder", c.name, c.value)
		}
	}
}

func TestLoadReportsPlaceholdersBeforeComplainingAboutAnythingElse(t *testing.T) {
	for _, kv := range [][2]string{
		{"SLACK_BOT_TOKEN", "xoxb-real"}, {"SLACK_APP_TOKEN", "xapp-real"},
		{"KEYCARD_ZONE_URL", "https://zone.example.com"}, {"KEYCARD_ZONE_ID", "zone_1"},
		{"KEYCARD_CLIENT_ID", "cid"}, {"KEYCARD_CLIENT_SECRET", "secret"},
		{"KEYCARD_RESOURCE", "urn:x:agent"}, {"BASE_URL", "https://your-agent.example.com"},
		{"ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef"}, {"STATE_SECRET", "s"},
		{"ENV_FILE", "/nonexistent/.env"},
	} {
		t.Setenv(kv[0], kv[1])
	}
	_, err := Load()
	if err == nil || !contains(err.Error(), "BASE_URL") {
		t.Fatalf("expected the placeholder BASE_URL to be reported, got %v", err)
	}
	if !contains(err.Error(), "placeholder") {
		t.Fatalf("error should say what is wrong: %v", err)
	}
}

func TestShadowedKeysAreReported(t *testing.T) {
	// The failure this makes visible: one `set -a; source .env` exports the old values, they win over
	// the file forever after in that shell, and editing the file changes nothing. Without this the
	// error names the value and cannot say the file was not the source of it.
	path := writeEnv(t, "BASE_URL=http://localhost:8080\nKEYCARD_ZONE_ID=from-file\n")
	t.Setenv("BASE_URL", "https://your-agent.example.com")
	_ = os.Unsetenv("KEYCARD_ZONE_ID")

	shadowed, err := LoadEnvFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(shadowed) != 1 || shadowed[0] != "BASE_URL" {
		t.Fatalf("expected BASE_URL to be reported as shadowed, got %#v", shadowed)
	}
	if got := os.Getenv("KEYCARD_ZONE_ID"); got != "from-file" {
		t.Fatalf("an unset key should still come from the file, got %q", got)
	}
}

func TestAnIdenticalValueIsNotShadowing(t *testing.T) {
	// Exporting the same value the file holds is not a conflict, and reporting it would be noise.
	path := writeEnv(t, "BASE_URL=http://localhost:8080\n")
	t.Setenv("BASE_URL", "http://localhost:8080")

	shadowed, err := LoadEnvFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(shadowed) != 0 {
		t.Fatalf("expected no shadowing, got %#v", shadowed)
	}
}

func TestThePlaceholderErrorExplainsShadowing(t *testing.T) {
	path := writeEnv(t, "BASE_URL=http://localhost:8080\n")
	for _, kv := range [][2]string{
		{"SLACK_BOT_TOKEN", "xoxb-real"}, {"SLACK_APP_TOKEN", "xapp-real"},
		{"KEYCARD_ZONE_URL", "https://zone.example.com"}, {"KEYCARD_ZONE_ID", "zone_1"},
		{"KEYCARD_CLIENT_ID", "cid"}, {"KEYCARD_CLIENT_SECRET", "secret"},
		{"KEYCARD_RESOURCE", "urn:x:agent"},
		{"ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef"}, {"STATE_SECRET", "s"},
		// the stale export
		{"BASE_URL", "https://your-agent.example.com"},
		{"ENV_FILE", path},
	} {
		t.Setenv(kv[0], kv[1])
	}
	_, err := Load()
	if err == nil {
		t.Fatal("expected an error")
	}
	if !contains(err.Error(), "shadowing .env") {
		t.Fatalf("error should say the value came from the environment: %v", err)
	}
	if !contains(err.Error(), "open a new") {
		t.Fatalf("error should say what to do: %v", err)
	}
}
