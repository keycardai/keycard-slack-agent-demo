package oauth

import (
	"errors"
	"testing"
	"time"
)

func TestStateRoundTripsCallerClaims(t *testing.T) {
	signer := NewStateSigner([]byte("secret"))
	token, err := signer.Sign(map[string]string{"team": "T1", "user": "U1"})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	claims, err := signer.Verify(token)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims["team"] != "T1" || claims["user"] != "U1" {
		t.Fatalf("unexpected claims: %#v", claims)
	}
	// The internal claims are an implementation detail and must not leak to callers, or a caller
	// round-tripping a verified map back through Sign would hit the reserved-key error.
	if _, ok := claims[stateClaimExpires]; ok {
		t.Fatalf("expiry claim leaked to caller: %#v", claims)
	}
	if _, ok := claims[stateClaimNonce]; ok {
		t.Fatalf("nonce claim leaked to caller: %#v", claims)
	}
}

func TestStateExpires(t *testing.T) {
	issued := time.Unix(1_700_000_000, 0)
	signer := NewStateSigner([]byte("secret")).WithClock(func() time.Time { return issued }, time.Minute)
	token, err := signer.Sign(map[string]string{"team": "T1"})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	// Still inside the window.
	fresh := signer.WithClock(func() time.Time { return issued.Add(30 * time.Second) }, time.Minute)
	if _, err := fresh.Verify(token); err != nil {
		t.Fatalf("expected a state inside the window to verify, got %v", err)
	}

	// Past the window. A valid signature is no longer sufficient, which is the whole point: without
	// this, a captured consent link is replayable for as long as the signing secret lives.
	stale := signer.WithClock(func() time.Time { return issued.Add(2 * time.Minute) }, time.Minute)
	if _, err := stale.Verify(token); !errors.Is(err, ErrStateExpired) {
		t.Fatalf("expected ErrStateExpired, got %v", err)
	}
}

func TestStateRejectsTamperedPayload(t *testing.T) {
	signer := NewStateSigner([]byte("secret"))
	token, err := signer.Sign(map[string]string{"team": "T1"})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	tampered := "eyJ0ZWFtIjoiVDIifQ." + token[len(token)-43:]
	if _, err := signer.Verify(tampered); !errors.Is(err, ErrStateSignature) {
		t.Fatalf("expected ErrStateSignature, got %v", err)
	}
}

func TestStateRejectsADifferentSecret(t *testing.T) {
	token, err := NewStateSigner([]byte("secret-a")).Sign(map[string]string{"team": "T1"})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := NewStateSigner([]byte("secret-b")).Verify(token); !errors.Is(err, ErrStateSignature) {
		t.Fatalf("expected ErrStateSignature, got %v", err)
	}
}

func TestStateRejectsReservedKeys(t *testing.T) {
	signer := NewStateSigner([]byte("secret"))
	for _, key := range []string{stateClaimExpires, stateClaimNonce} {
		if _, err := signer.Sign(map[string]string{key: "1"}); err == nil {
			t.Fatalf("expected Sign to reject the reserved key %q", key)
		}
	}
}

func TestStateNonceDiffersPerSignature(t *testing.T) {
	// Two states for the same request must be distinguishable, so a server that wants single-use
	// semantics has something to record.
	fixed := time.Unix(1_700_000_000, 0)
	signer := NewStateSigner([]byte("secret")).WithClock(func() time.Time { return fixed }, time.Minute)
	first, err := signer.Sign(map[string]string{"team": "T1"})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	second, err := signer.Sign(map[string]string{"team": "T1"})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if first == second {
		t.Fatal("expected identical claims at a fixed time to still produce different states")
	}
}

func TestStateRejectsMalformedTokens(t *testing.T) {
	signer := NewStateSigner([]byte("secret"))
	for _, token := range []string{"", "nodot", "a.b.c"} {
		if _, err := signer.Verify(token); !errors.Is(err, ErrStateMalformed) {
			t.Fatalf("expected ErrStateMalformed for %q, got %v", token, err)
		}
	}
}
