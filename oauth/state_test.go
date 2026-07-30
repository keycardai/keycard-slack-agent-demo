package oauth

import "testing"

func TestStateSignerRoundTrip(t *testing.T) {
	signer := NewStateSigner([]byte("secret"))
	state := map[string]string{"team": "T1", "user": "U1", "channel": "C1"}
	token, err := signer.Sign(state)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	out, err := signer.Verify(token)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if out["team"] != "T1" || out["user"] != "U1" || out["channel"] != "C1" {
		t.Fatalf("unexpected state: %+v", out)
	}
}

func TestStateSignerRejectsTamper(t *testing.T) {
	signer := NewStateSigner([]byte("secret"))
	state := map[string]string{"team": "T1"}
	token, err := signer.Sign(state)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	tampered := token + "x"
	if _, err := signer.Verify(tampered); err == nil {
		t.Fatal("expected error for tampered token")
	}
}
