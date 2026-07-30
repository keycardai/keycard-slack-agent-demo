package app

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// TokenFingerprint returns a short stable fingerprint for logging.
func TokenFingerprint(token string) string {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(trimmed))
	return hex.EncodeToString(sum[:8])
}
