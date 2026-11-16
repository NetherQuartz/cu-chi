package cryptobox

import (
	"bytes"
	"testing"
)

func TestSecretEncryption(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	b, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	plaintext := []byte("private SSH material")
	a := b.Seal(plaintext, "server:a")
	c := b.Seal(plaintext, "server:a")
	if bytes.Equal(a, c) || bytes.Contains(a, plaintext) {
		t.Fatal("encryption must use unique nonces and hide plaintext")
	}
	got, err := b.Open(a, "server:a")
	if err != nil || !bytes.Equal(got, plaintext) {
		t.Fatal("round trip failed", err)
	}
	if _, err = b.Open(a, "server:b"); err == nil {
		t.Fatal("ciphertext must be bound to its record")
	}
	a[len(a)-1] ^= 1
	if _, err = b.Open(a, "server:a"); err == nil {
		t.Fatal("tampering accepted")
	}
	if _, err = b.Open([]byte("short"), "server:a"); err == nil {
		t.Fatal("truncated ciphertext accepted")
	}
	other, _ := New(bytes.Repeat([]byte{8}, 32))
	if _, err = other.Open(c, "server:a"); err == nil {
		t.Fatal("wrong key accepted")
	}
}
