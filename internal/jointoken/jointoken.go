package jointoken

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
)

const (
	RoleDaemon = "daemon"
	RoleClient = "client"
)

// Mint returns a base64url HMAC join token for machineID and role.
func Mint(secret, machineID, role string) (string, error) {
	if secret == "" {
		return "", fmt.Errorf("relay auth secret is required")
	}
	if machineID == "" {
		return "", fmt.Errorf("machine_id is required")
	}
	if role != RoleDaemon && role != RoleClient {
		return "", fmt.Errorf("invalid role %q", role)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("kuma-join-v1|" + machineID + "|" + role))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// Valid reports whether token matches Mint(secret, machineID, role).
func Valid(secret, machineID, role, token string) bool {
	want, err := Mint(secret, machineID, role)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(want)) == 1
}
