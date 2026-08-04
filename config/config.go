package config

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

type Config struct {
	SlackBotToken               string
	SlackAppToken               string
	KeycardZoneURL              string
	KeycardZoneID               string
	KeycardAPIBaseURL           string
	KeycardClientID             string
	KeycardClientSecret         string
	KeycardServiceAccountID     string
	KeycardServiceAccountSecret string
	KeycardResource             string
	BaseURL                     string
	Port                        string
	DatabasePath                string
	EncryptionKey               []byte
	StateSecret                 []byte
	AnthropicAPIKey             string
	AnthropicModel              string
}

// Load reads configuration from the environment, after folding in a .env file when one exists so a
// filled-in file is not silently ignored.
func Load() (Config, error) {
	shadowed, err := LoadEnvFile(os.Getenv("ENV_FILE"))
	if err != nil {
		return Config{}, fmt.Errorf("reading env file: %w", err)
	}
	wasShadowed := make(map[string]bool, len(shadowed))
	for _, key := range shadowed {
		wasShadowed[key] = true
	}

	cfg := Config{
		SlackBotToken:               os.Getenv("SLACK_BOT_TOKEN"),
		SlackAppToken:               os.Getenv("SLACK_APP_TOKEN"),
		KeycardZoneURL:              strings.TrimRight(os.Getenv("KEYCARD_ZONE_URL"), "/"),
		KeycardZoneID:               strings.TrimSpace(os.Getenv("KEYCARD_ZONE_ID")),
		KeycardAPIBaseURL:           strings.TrimRight(os.Getenv("KEYCARD_API_BASE_URL"), "/"),
		KeycardClientID:             os.Getenv("KEYCARD_CLIENT_ID"),
		KeycardClientSecret:         os.Getenv("KEYCARD_CLIENT_SECRET"),
		KeycardServiceAccountID:     os.Getenv("KEYCARD_SERVICE_ACCOUNT_ID"),
		KeycardServiceAccountSecret: os.Getenv("KEYCARD_SERVICE_ACCOUNT_SECRET"),
		KeycardResource:             os.Getenv("KEYCARD_RESOURCE"),
		BaseURL:                     strings.TrimRight(os.Getenv("BASE_URL"), "/"),
		Port:                        os.Getenv("PORT"),
		DatabasePath:                os.Getenv("DATABASE_PATH"),
		EncryptionKey:               []byte(os.Getenv("ENCRYPTION_KEY")),
		StateSecret:                 []byte(os.Getenv("STATE_SECRET")),
		AnthropicAPIKey:             os.Getenv("ANTHROPIC_API_KEY"),
		AnthropicModel:              strings.TrimSpace(os.Getenv("ANTHROPIC_MODEL")),
	}

	if cfg.KeycardAPIBaseURL == "" {
		cfg.KeycardAPIBaseURL = "https://api.keycard.ai"
	}
	if cfg.Port == "" {
		cfg.Port = "8080"
	}
	if cfg.DatabasePath == "" {
		// A path that works wherever the binary runs. The deployed case mounts a volume and sets
		// DATABASE_PATH explicitly; defaulting to /data meant an unset value failed on any machine
		// without that directory, which is every developer machine.
		cfg.DatabasePath = "slack-agent.db"
	}

	var missing []string
	if cfg.SlackBotToken == "" {
		missing = append(missing, "SLACK_BOT_TOKEN")
	}
	if cfg.SlackAppToken == "" {
		missing = append(missing, "SLACK_APP_TOKEN")
	}
	if cfg.KeycardZoneURL == "" {
		missing = append(missing, "KEYCARD_ZONE_URL")
	}
	if cfg.KeycardZoneID == "" {
		missing = append(missing, "KEYCARD_ZONE_ID")
	}
	if cfg.KeycardClientID == "" {
		missing = append(missing, "KEYCARD_CLIENT_ID")
	}
	if cfg.KeycardClientSecret == "" {
		missing = append(missing, "KEYCARD_CLIENT_SECRET")
	}
	if cfg.KeycardResource == "" {
		missing = append(missing, "KEYCARD_RESOURCE")
	}
	if cfg.BaseURL == "" {
		missing = append(missing, "BASE_URL")
	}
	if len(cfg.EncryptionKey) == 0 {
		missing = append(missing, "ENCRYPTION_KEY")
	}
	if len(cfg.StateSecret) == 0 {
		missing = append(missing, "STATE_SECRET")
	}
	// Unedited placeholders from .env.example. These are worse than a missing value, because they are
	// present, so nothing complains and the failure surfaces much later: a placeholder BASE_URL sends
	// the consent redirect to a domain you do not own, and a literal "..." service-account id looks
	// configured enough to take the service-account branch and then fails every inventory read, which
	// presents as an agent with no tools.
	var placeholders []string
	for name, value := range map[string]string{
		"SLACK_BOT_TOKEN":                cfg.SlackBotToken,
		"SLACK_APP_TOKEN":                cfg.SlackAppToken,
		"KEYCARD_ZONE_URL":               cfg.KeycardZoneURL,
		"KEYCARD_ZONE_ID":                cfg.KeycardZoneID,
		"KEYCARD_CLIENT_ID":              cfg.KeycardClientID,
		"KEYCARD_CLIENT_SECRET":          cfg.KeycardClientSecret,
		"KEYCARD_RESOURCE":               cfg.KeycardResource,
		"KEYCARD_SERVICE_ACCOUNT_ID":     cfg.KeycardServiceAccountID,
		"KEYCARD_SERVICE_ACCOUNT_SECRET": cfg.KeycardServiceAccountSecret,
		"BASE_URL":                       cfg.BaseURL,
		"ANTHROPIC_API_KEY":              cfg.AnthropicAPIKey,
		"ENCRYPTION_KEY":                 string(cfg.EncryptionKey),
		"STATE_SECRET":                   string(cfg.StateSecret),
	} {
		if isPlaceholder(name, value) {
			if wasShadowed[name] {
				name += " (from the environment, shadowing .env)"
			}
			placeholders = append(placeholders, name)
		}
	}
	sort.Strings(placeholders)
	if len(placeholders) > 0 {
		hint := " (leave a value empty if you do not want it, rather than leaving the example text)"
		if len(shadowed) > 0 {
			// The stale-export case. Editing the file cannot help while the old value is exported.
			hint = "; values already exported in your shell take precedence over .env, so open a new" +
				" shell or unset them"
		}
		return cfg, errors.New("these still hold placeholder values from .env.example: " +
			strings.Join(placeholders, ", ") + hint)
	}

	if len(missing) > 0 {
		envFile := os.Getenv("ENV_FILE")
		if envFile == "" {
			envFile = DefaultEnvFile
		}
		hint := ""
		if _, err := os.Stat(envFile); err != nil {
			// Naming the file it looked for turns "everything is missing" from a mystery into an
			// instruction. Every variable absent at once nearly always means the file was not found.
			hint = " (no " + envFile + " found; copy .env.example to .env, or export the values)"
		}
		return cfg, errors.New("missing required env vars: " + strings.Join(missing, ", ") + hint)
	}

	return cfg, nil
}

// isPlaceholder reports whether a value is still the example text rather than a real one.
//
// Narrow on purpose. A real secret can contain almost anything, so this matches the exact strings
// .env.example ships and the "..." convention, not a general shape.
func isPlaceholder(name, value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false // absent is a different problem, reported separately
	}
	switch trimmed {
	case "...", "xoxb-...", "xapp-...", "change-me", "https://your-agent.example.com":
		return true
	}
	// The paste error that produced /DATABASE_PATH=./agent.db: the value repeats its own key. No
	// legitimate value does that, and base64 padding or a connection string will not match, because
	// this looks for the variable's own name followed by an equals sign.
	if strings.Contains(trimmed, name+"=") {
		return true
	}
	return strings.Contains(trimmed, "change-me-32-bytes") ||
		strings.Contains(trimmed, "your-agent.example.com")
}
