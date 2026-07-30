package crypto

import "testing"

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	cipher, err := NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher error: %v", err)
	}

	plaintext := []byte("hello sandbox")
	ciphertext, err := cipher.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt error: %v", err)
	}

	out, err := cipher.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt error: %v", err)
	}
	if string(out) != string(plaintext) {
		t.Fatalf("expected %q, got %q", plaintext, out)
	}
}

func TestNewCipherKeySize(t *testing.T) {
	_, err := NewCipher([]byte("short"))
	if err == nil {
		t.Fatal("expected error for short key")
	}
}
