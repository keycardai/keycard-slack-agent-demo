package config

import (
	"bufio"
	"os"
	"strings"
)

// DefaultEnvFile is loaded at startup when it exists. Override with ENV_FILE.
const DefaultEnvFile = ".env"

// LoadEnvFile reads a KEY=value file into the process environment.
//
// Go has no convention for this, so a filled-in .env did nothing and every variable came back
// missing at once. That reads like the file was ignored, which it was, and the error message cannot
// tell you so because os.Getenv has no idea a file exists.
//
// Values already present in the environment always win. A deployment sets real environment variables
// and must never be silently overridden by a file that happened to ship in the image.
//
// Missing file is not an error: production has no .env.
//
// Returns the keys the file defined but did not set, because the environment already had them. That
// list is the answer to "I fixed the file and nothing changed": one `set -a; source .env` leaves the
// old values exported, and they win forever afterwards in that shell.
func LoadEnvFile(path string) (shadowed []string, err error) {
	if strings.TrimSpace(path) == "" {
		path = DefaultEnvFile
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := parseEnvLine(scanner.Text())
		if !ok {
			continue
		}
		if existing, already := os.LookupEnv(key); already {
			if existing != value {
				shadowed = append(shadowed, key)
			}
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return nil, err
		}
	}
	return shadowed, scanner.Err()
}

// parseEnvLine returns the key and value from one line, or ok=false for blanks and comments.
func parseEnvLine(line string) (key, value string, ok bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", "", false
	}
	// `export KEY=value` is common in a hand-written file and harmless to accept.
	trimmed = strings.TrimPrefix(trimmed, "export ")

	name, raw, found := strings.Cut(trimmed, "=")
	if !found {
		return "", "", false
	}
	key = strings.TrimSpace(name)
	if key == "" {
		return "", "", false
	}

	value = strings.TrimSpace(raw)
	// Strip one matching pair of quotes. Inside quotes the value is taken literally, including a #,
	// which is why the comment strip below only applies to bare values.
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') ||
			(value[0] == '\'' && value[len(value)-1] == '\'') {
			return key, value[1 : len(value)-1], true
		}
	}
	if hash := strings.Index(value, " #"); hash >= 0 {
		value = strings.TrimSpace(value[:hash])
	}
	return key, value, true
}
