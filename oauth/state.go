package oauth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// StateTTL is how long a signed OAuth state parameter stays valid.
//
// A signature alone is not enough. Without an expiry, a state value stays valid for the life of the
// signing secret, so a link captured from a browser history or a Slack message can be replayed
// indefinitely. Ten minutes is long enough for a person to finish a consent screen.
const StateTTL = 10 * time.Minute

// Reserved claim names. Callers never set or read these.
const (
	stateClaimExpires = "_exp"
	stateClaimNonce   = "_nonce"
)

var (
	// ErrStateExpired means the signature was valid but the state is too old to accept.
	ErrStateExpired = errors.New("state expired")
	// ErrStateMalformed means the state was not produced by Sign.
	ErrStateMalformed = errors.New("invalid state format")
	// ErrStateSignature means the state was tampered with or signed by a different secret.
	ErrStateSignature = errors.New("invalid state signature")
)

type StateSigner struct {
	secret []byte
	now    func() time.Time
	ttl    time.Duration
}

func NewStateSigner(secret []byte) *StateSigner {
	return &StateSigner{secret: secret, now: time.Now, ttl: StateTTL}
}

// WithClock overrides the clock and TTL. For tests.
func (s *StateSigner) WithClock(now func() time.Time, ttl time.Duration) *StateSigner {
	return &StateSigner{secret: s.secret, now: now, ttl: ttl}
}

func (s *StateSigner) Sign(state map[string]string) (string, error) {
	claims := make(map[string]string, len(state)+2)
	for key, value := range state {
		if key == stateClaimExpires || key == stateClaimNonce {
			return "", errors.New("state key " + key + " is reserved")
		}
		claims[key] = value
	}
	claims[stateClaimExpires] = strconv.FormatInt(s.clock().Add(s.lifetime()).Unix(), 10)

	// A nonce makes two states for the same request distinguishable, so a server that wants
	// single-use semantics has something to record. Nothing here stores it yet.
	nonce, err := randomNonce()
	if err != nil {
		return "", err
	}
	claims[stateClaimNonce] = nonce

	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return encoded + "." + s.sign(encoded), nil
}

func (s *StateSigner) Verify(token string) (map[string]string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return nil, ErrStateMalformed
	}
	payload := parts[0]
	sig := parts[1]

	if !hmac.Equal([]byte(sig), []byte(s.sign(payload))) {
		return nil, ErrStateSignature
	}

	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil, err
	}
	var claims map[string]string
	if err := json.Unmarshal(raw, &claims); err != nil {
		return nil, err
	}

	// Check the expiry only after the signature, so an attacker learns nothing from the ordering.
	expires, err := strconv.ParseInt(claims[stateClaimExpires], 10, 64)
	if err != nil {
		return nil, ErrStateMalformed
	}
	if s.clock().After(time.Unix(expires, 0)) {
		return nil, ErrStateExpired
	}

	delete(claims, stateClaimExpires)
	delete(claims, stateClaimNonce)
	return claims, nil
}

func (s *StateSigner) sign(payload string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(payload))
	sum := mac.Sum(nil)
	return base64.RawURLEncoding.EncodeToString(sum)
}

func (s *StateSigner) clock() time.Time {
	if s.now == nil {
		return time.Now()
	}
	return s.now()
}

func (s *StateSigner) lifetime() time.Duration {
	if s.ttl <= 0 {
		return StateTTL
	}
	return s.ttl
}

func randomNonce() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
