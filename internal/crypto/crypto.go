package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

const KeySize = 32

var (
	ErrInvalidKeyLength = errors.New("invalid key length: must be 32 bytes")
	ErrCiphertextShort  = errors.New("ciphertext too short")
)

// GenerateKey creates a 32-byte AES-256 key.
func GenerateKey() ([]byte, error) {
	key := make([]byte, KeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	return key, nil
}

// EncodeKey serializes a key as URL-safe base64 without padding.
func EncodeKey(key []byte) string {
	return base64.RawURLEncoding.EncodeToString(key)
}

// DecodeKey parses a URL-safe base64 key (with or without padding).
func DecodeKey(encoded string) ([]byte, error) {
	key, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		key, err = base64.URLEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("decode key: %w", err)
		}
	}
	if len(key) != KeySize {
		return nil, ErrInvalidKeyLength
	}
	return key, nil
}

// Cipher is a reusable AES-256-GCM AEAD bound to a single key.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher builds a Cipher from a 32-byte key.
func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != KeySize {
		return nil, ErrInvalidKeyLength
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: gcm}, nil
}

// Encrypt encrypts plaintext and returns nonce || ciphertext.
// aad is authenticated but not encrypted (typically machine_id).
func (c *Cipher) Encrypt(plaintext, aad []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return c.aead.Seal(nonce, nonce, plaintext, aad), nil
}

// Decrypt decrypts a nonce || ciphertext blob produced by Encrypt.
func (c *Cipher) Decrypt(blob, aad []byte) ([]byte, error) {
	nonceSize := c.aead.NonceSize()
	if len(blob) < nonceSize {
		return nil, ErrCiphertextShort
	}
	nonce, ciphertext := blob[:nonceSize], blob[nonceSize:]
	return c.aead.Open(nil, nonce, ciphertext, aad)
}

// Encrypt is a one-shot helper that builds a Cipher for a single operation.
func Encrypt(key, plaintext, aad []byte) ([]byte, error) {
	c, err := NewCipher(key)
	if err != nil {
		return nil, err
	}
	return c.Encrypt(plaintext, aad)
}

// Decrypt is a one-shot helper that builds a Cipher for a single operation.
func Decrypt(key, blob, aad []byte) ([]byte, error) {
	c, err := NewCipher(key)
	if err != nil {
		return nil, err
	}
	return c.Decrypt(blob, aad)
}
