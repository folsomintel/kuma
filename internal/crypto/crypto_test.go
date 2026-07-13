package crypto

import (
	"bytes"
	"errors"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	aad := []byte("machine-1")
	plaintext := []byte(`{"type":"input","seq":1,"session_id":"abc","data":"aGVsbG8="}`)
	blob, err := Encrypt(key, plaintext, aad)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Equal(blob, plaintext) {
		t.Fatal("ciphertext must differ from plaintext")
	}

	got, err := Decrypt(key, blob, aad)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("got %q, want %q", got, plaintext)
	}
}

func TestEncryptUsesFreshNonces(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	aad := []byte("m")
	plaintext := []byte("same plaintext")
	a, err := Encrypt(key, plaintext, aad)
	if err != nil {
		t.Fatalf("Encrypt a: %v", err)
	}
	b, err := Encrypt(key, plaintext, aad)
	if err != nil {
		t.Fatalf("Encrypt b: %v", err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("expected different ciphertext for identical plaintext")
	}
}

func TestDecryptRejectsWrongAAD(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	blob, err := Encrypt(key, []byte("secret"), []byte("machine-a"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := Decrypt(key, blob, []byte("machine-b")); err == nil {
		t.Fatal("expected decrypt failure for wrong aad")
	}
}

func TestDecryptRejectsTampering(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	blob, err := Encrypt(key, []byte("secret"), nil)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	blob[len(blob)-1] ^= 0xff

	if _, err := Decrypt(key, blob, nil); err == nil {
		t.Fatal("expected decrypt failure for tampered ciphertext")
	}
}

func TestDecryptRejectsWrongKey(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	other, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey other: %v", err)
	}

	blob, err := Encrypt(key, []byte("secret"), nil)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := Decrypt(other, blob, nil); err == nil {
		t.Fatal("expected decrypt failure for wrong key")
	}
}

func TestDecryptRejectsShortCiphertext(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if _, err := Decrypt(key, []byte("short"), nil); !errors.Is(err, ErrCiphertextShort) {
		t.Fatalf("expected ErrCiphertextShort, got %v", err)
	}
}

func TestKeyEncodingRoundTrip(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	encoded := EncodeKey(key)
	got, err := DecodeKey(encoded)
	if err != nil {
		t.Fatalf("DecodeKey: %v", err)
	}
	if !bytes.Equal(got, key) {
		t.Fatalf("decoded key mismatch")
	}
}

func TestDecodeKeyRejectsInvalidLength(t *testing.T) {
	short := EncodeKey([]byte("0123456789abcdef0123456789ab")) // 28 bytes
	if _, err := DecodeKey(short); err == nil {
		t.Fatal("expected invalid key length error")
	}
}

func TestCipherReusable(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	aad := []byte("machine")
	for i := 0; i < 50; i++ {
		plain := []byte("msg-" + string(rune('a'+i%26)))
		blob, err := c.Encrypt(plain, aad)
		if err != nil {
			t.Fatal(err)
		}
		got, err := c.Decrypt(blob, aad)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, plain) {
			t.Fatalf("round-trip %d mismatch", i)
		}
	}
}

func TestCompatibilityVectorZeroKey(t *testing.T) {
	zeroKey := make([]byte, KeySize)
	const want = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if got := EncodeKey(zeroKey); got != want {
		t.Fatalf("EncodeKey(zero)=%q want %q", got, want)
	}
	decoded, err := DecodeKey(want)
	if err != nil {
		t.Fatalf("DecodeKey: %v", err)
	}
	if !bytes.Equal(decoded, zeroKey) {
		t.Fatal("zero key decode mismatch")
	}
}
