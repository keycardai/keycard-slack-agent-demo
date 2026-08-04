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

	if err := LoadEnvFile(path); err != nil {
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

	if err := LoadEnvFile(path); err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := os.Getenv("KEYCARD_ZONE_ID"); got != "from-the-platform" {
		t.Fatalf("the file overrode the real environment: %q", got)
	}
}

func TestAMissingFileIsNotAnError(t *testing.T) {
	// Production has no .env, so its absence cannot be fatal.
	if err := LoadEnvFile(filepath.Join(t.TempDir(), "nope.env")); err != nil {
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
