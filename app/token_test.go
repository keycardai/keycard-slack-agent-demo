package app

import "testing"

func TestTokenFingerprintStable(t *testing.T) {
	fp1 := TokenFingerprint("my-secret-token")
	fp2 := TokenFingerprint("my-secret-token")
	if fp1 != fp2 {
		t.Fatalf("expected stable fingerprint, got %q and %q", fp1, fp2)
	}
	if fp1 == "" {
		t.Fatal("expected non-empty fingerprint")
	}
}

func TestTokenFingerprintDifferentTokens(t *testing.T) {
	fp1 := TokenFingerprint("token-a")
	fp2 := TokenFingerprint("token-b")
	if fp1 == fp2 {
		t.Fatalf("expected different fingerprints for different tokens, got %q", fp1)
	}
}

func TestTokenFingerprintEmpty(t *testing.T) {
	if fp := TokenFingerprint(""); fp != "" {
		t.Fatalf("expected empty fingerprint for empty token, got %q", fp)
	}
}

func TestTokenFingerprintWhitespace(t *testing.T) {
	if fp := TokenFingerprint("   "); fp != "" {
		t.Fatalf("expected empty fingerprint for whitespace-only token, got %q", fp)
	}
}

func TestTokenFingerprintTrimsWhitespace(t *testing.T) {
	fp1 := TokenFingerprint("token")
	fp2 := TokenFingerprint("  token  ")
	if fp1 != fp2 {
		t.Fatalf("expected trimming, got %q and %q", fp1, fp2)
	}
}

func TestTokenFingerprintLength(t *testing.T) {
	fp := TokenFingerprint("some-token")
	// sha256[:8] = 8 bytes = 16 hex chars
	if len(fp) != 16 {
		t.Fatalf("expected 16-char hex fingerprint, got %d chars: %q", len(fp), fp)
	}
}
