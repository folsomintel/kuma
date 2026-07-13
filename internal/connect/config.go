package connect

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"kuma/internal/crypto"
)

// Remote is a saved kumad endpoint the client can dial.
type Remote struct {
	MachineID string `json:"machine_id"`
	Key       string `json:"key"`
	RelayURL  string `json:"relay_url"`
	JoinToken string `json:"join_token"`
}

// RemotesFile is the on-disk remotes registry.
type RemotesFile struct {
	Default string            `json:"default,omitempty"`
	Remotes map[string]Remote `json:"remotes"`
}

// Credentials are resolved connection parameters (from file, flags, or env).
type Credentials struct {
	MachineID string
	Key       string
	RelayURL  string
	JoinToken string
	keyBytes  []byte
}

// KeyBytes returns the decoded E2E key.
func (c *Credentials) KeyBytes() []byte {
	return c.keyBytes
}

// DefaultRemotesPath returns ~/.config/kuma/remotes.json (or platform equivalent).
func DefaultRemotesPath() (string, error) {
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "kuma", "remotes.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "kuma", "remotes.json"), nil
}

// LoadRemotesFile reads remotes.json. Missing file yields an empty registry.
func LoadRemotesFile(path string) (*RemotesFile, error) {
	if path == "" {
		var err error
		path, err = DefaultRemotesPath()
		if err != nil {
			return nil, err
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &RemotesFile{Remotes: map[string]Remote{}}, nil
		}
		return nil, fmt.Errorf("read remotes %s: %w", path, err)
	}
	var f RemotesFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse remotes %s: %w", path, err)
	}
	if f.Remotes == nil {
		f.Remotes = map[string]Remote{}
	}
	return &f, nil
}

// SaveRemotesFile writes remotes.json with mode 0600.
func SaveRemotesFile(path string, f *RemotesFile) error {
	if path == "" {
		var err error
		path, err = DefaultRemotesPath()
		if err != nil {
			return err
		}
	}
	if f.Remotes == nil {
		f.Remotes = map[string]Remote{}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

// AddRemote upserts a named remote and optionally marks it default.
func AddRemote(path, name string, remote Remote, makeDefault bool) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("remote name is required")
	}
	if err := remote.Validate(); err != nil {
		return err
	}
	remote.MachineID = strings.TrimSpace(remote.MachineID)
	remote.Key = strings.TrimSpace(remote.Key)
	remote.RelayURL = strings.TrimSpace(remote.RelayURL)
	remote.JoinToken = strings.TrimSpace(remote.JoinToken)
	f, err := LoadRemotesFile(path)
	if err != nil {
		return err
	}
	f.Remotes[name] = remote
	if makeDefault || f.Default == "" {
		f.Default = name
	}
	return SaveRemotesFile(path, f)
}

// RemoveRemote deletes a named remote.
func RemoveRemote(path, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("remote name is required")
	}
	f, err := LoadRemotesFile(path)
	if err != nil {
		return err
	}
	if _, ok := f.Remotes[name]; !ok {
		return fmt.Errorf("remote %q not found", name)
	}
	delete(f.Remotes, name)
	if f.Default == name {
		f.Default = ""
		for n := range f.Remotes {
			f.Default = n
			break
		}
	}
	return SaveRemotesFile(path, f)
}

// Validate checks required remote fields and key encoding.
func (r Remote) Validate() error {
	machineID := strings.TrimSpace(r.MachineID)
	key := strings.TrimSpace(r.Key)
	relayURL := strings.TrimSpace(r.RelayURL)
	joinToken := strings.TrimSpace(r.JoinToken)
	if machineID == "" {
		return fmt.Errorf("machine_id is required")
	}
	if strings.Contains(machineID, "/") {
		return fmt.Errorf("machine_id must not contain '/'")
	}
	if key == "" {
		return fmt.Errorf("key is required")
	}
	if _, err := crypto.DecodeKey(key); err != nil {
		return fmt.Errorf("key: %w", err)
	}
	if relayURL == "" {
		return fmt.Errorf("relay_url is required")
	}
	if joinToken == "" {
		return fmt.Errorf("join_token is required")
	}
	return nil
}

// ResolveCredentials merges remote name, file, env, and explicit flag overrides.
// Explicit non-empty flags win over env, which wins over the named remote.
func ResolveCredentials(path, remoteName, machineID, key, relayURL, joinToken string) (*Credentials, error) {
	if path == "" {
		var err error
		path, err = DefaultRemotesPath()
		if err != nil {
			return nil, err
		}
	}

	var base Remote
	f, err := LoadRemotesFile(path)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(remoteName)
	if name == "" {
		name = strings.TrimSpace(os.Getenv("KUMA_CONNECT_REMOTE"))
	}
	if name == "" {
		name = strings.TrimSpace(f.Default)
	}
	if name != "" {
		r, ok := f.Remotes[name]
		if !ok {
			return nil, fmt.Errorf("remote %q not found", name)
		}
		base = r
	}

	cred := &Credentials{
		MachineID: firstNonEmpty(machineID, os.Getenv("KUMA_MACHINE_ID"), base.MachineID),
		Key:       firstNonEmpty(key, os.Getenv("KUMA_KEY"), base.Key),
		RelayURL:  firstNonEmpty(relayURL, os.Getenv("KUMA_RELAY_URL"), base.RelayURL),
		JoinToken: firstNonEmpty(joinToken, os.Getenv("KUMA_JOIN_TOKEN"), base.JoinToken),
	}
	if err := cred.Validate(); err != nil {
		if name == "" && machineID == "" && key == "" && relayURL == "" && joinToken == "" {
			return nil, fmt.Errorf("no remote configured; pass credentials or run: kuma-connect remotes add <name>")
		}
		return nil, err
	}
	return cred, nil
}

// Validate decodes the E2E key and checks required fields.
func (c *Credentials) Validate() error {
	c.MachineID = strings.TrimSpace(c.MachineID)
	c.Key = strings.TrimSpace(c.Key)
	c.RelayURL = strings.TrimSpace(c.RelayURL)
	c.JoinToken = strings.TrimSpace(c.JoinToken)
	if c.MachineID == "" {
		return fmt.Errorf("machine_id is required")
	}
	if strings.Contains(c.MachineID, "/") {
		return fmt.Errorf("machine_id must not contain '/'")
	}
	if c.Key == "" {
		return fmt.Errorf("key is required")
	}
	keyBytes, err := crypto.DecodeKey(c.Key)
	if err != nil {
		return fmt.Errorf("key: %w", err)
	}
	c.keyBytes = keyBytes
	if c.RelayURL == "" {
		return fmt.Errorf("relay_url is required")
	}
	if c.JoinToken == "" {
		return fmt.Errorf("join_token is required")
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
