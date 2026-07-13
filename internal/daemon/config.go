package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/folsomintel/kuma/internal/crypto"
	"github.com/folsomintel/kuma/internal/jointoken"
)

func mintDaemonJoinToken(secret, machineID string) (string, error) {
	return jointoken.Mint(secret, machineID, jointoken.RoleDaemon)
}

const (
	defaultHistoryLimit = 64 * 1024
	minHistoryLimit     = 1024
	maxHistoryLimit     = 1 << 20 // 1 MiB
	defaultMaxSessions  = 8
	defaultMinBackoff   = time.Second
	defaultMaxBackoff   = 30 * time.Second
)

// Config holds kumad runtime configuration.
type Config struct {
	MachineID    string        `json:"machine_id"`
	Key          string        `json:"key"`
	RelayURL     string        `json:"relay_url"`
	JoinToken    string        `json:"join_token,omitempty"`
	CWDRoot      string        `json:"cwd_root,omitempty"`
	HistoryLimit int           `json:"history_limit,omitempty"`
	MaxSessions  int           `json:"max_sessions,omitempty"`
	MinBackoff   time.Duration `json:"-"`
	MaxBackoff   time.Duration `json:"-"`

	keyBytes []byte
}

type fileConfig struct {
	MachineID    string `json:"machine_id"`
	Key          string `json:"key"`
	RelayURL     string `json:"relay_url"`
	JoinToken    string `json:"join_token,omitempty"`
	CWDRoot      string `json:"cwd_root,omitempty"`
	HistoryLimit int    `json:"history_limit,omitempty"`
	MaxSessions  int    `json:"max_sessions,omitempty"`
}

// DefaultConfigPath returns ~/.config/kuma/config.json on Unix
// and the platform user config directory elsewhere.
func DefaultConfigPath() (string, error) {
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "kuma", "config.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "kuma", "config.json"), nil
}

// LoadConfig loads JSON config and applies environment/flag overrides.
// Explicit values (non-empty flags) win over env, which wins over file.
func LoadConfig(path, machineID, key, relayURL, joinToken string) (*Config, error) {
	cfg := &Config{
		HistoryLimit: defaultHistoryLimit,
		MaxSessions:  defaultMaxSessions,
		MinBackoff:   defaultMinBackoff,
		MaxBackoff:   defaultMaxBackoff,
	}

	if path == "" {
		var err error
		path, err = DefaultConfigPath()
		if err != nil {
			return nil, err
		}
	}

	if data, err := os.ReadFile(path); err == nil {
		var fc fileConfig
		if err := json.Unmarshal(data, &fc); err != nil {
			return nil, fmt.Errorf("parse config %s: %w", path, err)
		}
		cfg.MachineID = fc.MachineID
		cfg.Key = fc.Key
		cfg.RelayURL = fc.RelayURL
		cfg.JoinToken = fc.JoinToken
		cfg.CWDRoot = fc.CWDRoot
		if fc.HistoryLimit > 0 {
			cfg.HistoryLimit = fc.HistoryLimit
		}
		if fc.MaxSessions > 0 {
			cfg.MaxSessions = fc.MaxSessions
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	if v := os.Getenv("KUMA_MACHINE_ID"); v != "" {
		cfg.MachineID = v
	}
	if v := os.Getenv("KUMA_KEY"); v != "" {
		cfg.Key = v
	}
	if v := os.Getenv("KUMA_RELAY_URL"); v != "" {
		cfg.RelayURL = v
	}
	if v := os.Getenv("KUMA_JOIN_TOKEN"); v != "" {
		cfg.JoinToken = v
	}
	if v := os.Getenv("KUMA_CWD_ROOT"); v != "" {
		cfg.CWDRoot = v
	}

	if machineID != "" {
		cfg.MachineID = machineID
	}
	if key != "" {
		cfg.Key = key
	}
	if relayURL != "" {
		cfg.RelayURL = relayURL
	}
	if joinToken != "" {
		cfg.JoinToken = joinToken
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Validate checks required fields and decodes the E2E key.
func (c *Config) Validate() error {
	c.MachineID = strings.TrimSpace(c.MachineID)
	c.Key = strings.TrimSpace(c.Key)
	c.RelayURL = strings.TrimSpace(c.RelayURL)
	c.JoinToken = strings.TrimSpace(c.JoinToken)
	c.CWDRoot = strings.TrimSpace(c.CWDRoot)

	if c.MachineID == "" {
		return fmt.Errorf("machine_id is required")
	}
	if strings.Contains(c.MachineID, "/") {
		return fmt.Errorf("machine_id must not contain '/'")
	}
	if c.Key == "" {
		return fmt.Errorf("key is required")
	}
	if c.RelayURL == "" {
		return fmt.Errorf("relay_url is required")
	}
	if c.JoinToken == "" {
		return fmt.Errorf("join_token is required")
	}
	keyBytes, err := crypto.DecodeKey(c.Key)
	if err != nil {
		return fmt.Errorf("key: %w", err)
	}
	c.keyBytes = keyBytes

	if c.CWDRoot != "" {
		if !filepath.IsAbs(c.CWDRoot) {
			return fmt.Errorf("cwd_root must be an absolute path")
		}
		c.CWDRoot = filepath.Clean(c.CWDRoot)
	}

	if c.HistoryLimit <= 0 {
		c.HistoryLimit = defaultHistoryLimit
	}
	if c.HistoryLimit < minHistoryLimit {
		c.HistoryLimit = minHistoryLimit
	}
	if c.HistoryLimit > maxHistoryLimit {
		c.HistoryLimit = maxHistoryLimit
	}
	if c.MaxSessions <= 0 {
		c.MaxSessions = defaultMaxSessions
	}
	if c.MinBackoff <= 0 {
		c.MinBackoff = defaultMinBackoff
	}
	if c.MaxBackoff < c.MinBackoff {
		c.MaxBackoff = defaultMaxBackoff
	}
	return nil
}

// KeyBytes returns the decoded E2E key. Never log this value.
func (c *Config) KeyBytes() []byte {
	return c.keyBytes
}

// Save writes the config to path with restrictive permissions.
func (c *Config) Save(path string) error {
	if path == "" {
		var err error
		path, err = DefaultConfigPath()
		if err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	fc := fileConfig{
		MachineID:    c.MachineID,
		Key:          c.Key,
		RelayURL:     c.RelayURL,
		JoinToken:    c.JoinToken,
		CWDRoot:      c.CWDRoot,
		HistoryLimit: c.HistoryLimit,
		MaxSessions:  c.MaxSessions,
	}
	data, err := json.MarshalIndent(fc, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

// InitConfig generates a new local config (machine id + E2E key).
// relayURL defaults to ws://127.0.0.1:8080 when empty.
// Provide joinToken, or authSecret to mint a daemon join token after machine_id is chosen.
// If a config already exists and force is false, it loads and returns that config
// with created=false.
func InitConfig(path, relayURL, joinToken, authSecret string, force bool) (cfg *Config, wrote string, created bool, err error) {
	if path == "" {
		path, err = DefaultConfigPath()
		if err != nil {
			return nil, "", false, err
		}
	}
	if _, statErr := os.Stat(path); statErr == nil && !force {
		existing, loadErr := LoadConfig(path, "", "", "", "")
		if loadErr != nil {
			return nil, path, false, fmt.Errorf("config exists at %s but failed to load: %w", path, loadErr)
		}
		return existing, path, false, nil
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return nil, path, false, statErr
	}

	if strings.TrimSpace(relayURL) == "" {
		relayURL = "ws://127.0.0.1:8080"
	}
	joinToken = strings.TrimSpace(joinToken)
	authSecret = strings.TrimSpace(authSecret)

	key, err := crypto.GenerateKey()
	if err != nil {
		return nil, path, false, err
	}
	machineKey, err := crypto.GenerateKey()
	if err != nil {
		return nil, path, false, err
	}
	// Stable, URL-safe machine id derived from random bytes.
	machineID := crypto.EncodeKey(machineKey)[:22]

	if joinToken == "" {
		if authSecret == "" {
			return nil, path, false, fmt.Errorf("join_token is required (pass -join-token / KUMA_JOIN_TOKEN, or -auth-secret / KUMA_RELAY_AUTH_SECRET)")
		}
		joinToken, err = mintDaemonJoinToken(authSecret, machineID)
		if err != nil {
			return nil, path, false, err
		}
	}

	cfg = &Config{
		MachineID:    machineID,
		Key:          crypto.EncodeKey(key),
		RelayURL:     relayURL,
		JoinToken:    joinToken,
		HistoryLimit: defaultHistoryLimit,
		MaxSessions:  defaultMaxSessions,
		MinBackoff:   defaultMinBackoff,
		MaxBackoff:   defaultMaxBackoff,
	}
	if err := cfg.Validate(); err != nil {
		return nil, path, false, err
	}
	if err := cfg.Save(path); err != nil {
		return nil, path, false, err
	}
	return cfg, path, true, nil
}

// ValidateCWD checks an optional session working directory against policy.
// When cwd_root is set, both paths are resolved with EvalSymlinks so a
// symlink under the root cannot escape to an outside directory.
func (c *Config) ValidateCWD(cwd string) error {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return nil
	}
	if !filepath.IsAbs(cwd) {
		return fmt.Errorf("cwd must be an absolute path")
	}
	cwd = filepath.Clean(cwd)
	if c.CWDRoot == "" {
		return nil
	}

	root, err := filepath.EvalSymlinks(c.CWDRoot)
	if err != nil {
		return fmt.Errorf("cwd_root: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return fmt.Errorf("cwd: %w", err)
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil {
		return fmt.Errorf("cwd outside cwd_root")
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("cwd outside cwd_root")
	}
	return nil
}
