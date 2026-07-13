package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/folsomintel/kuma/internal/crypto"
)

func TestLoadConfigOverrides(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	encoded := crypto.EncodeKey(key)

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
  "machine_id": "file-machine",
  "key": "` + encoded + `",
  "relay_url": "ws://file.example/relay",
  "join_token": "file-token"
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("KUMA_MACHINE_ID", "env-machine")
	t.Setenv("KUMA_RELAY_URL", "ws://env.example/relay")

	cfg, err := LoadConfig(path, "flag-machine", "", "", "")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.MachineID != "flag-machine" {
		t.Fatalf("machine_id=%q", cfg.MachineID)
	}
	if cfg.RelayURL != "ws://env.example/relay" {
		t.Fatalf("relay_url=%q", cfg.RelayURL)
	}
	if cfg.JoinToken != "file-token" {
		t.Fatalf("join_token=%q", cfg.JoinToken)
	}
	if string(cfg.KeyBytes()) != string(key) {
		t.Fatal("key bytes mismatch")
	}
}

func TestLoadConfigRequiresFields(t *testing.T) {
	_, err := LoadConfig(filepath.Join(t.TempDir(), "missing.json"), "", "", "", "")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestInitConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kuma", "config.json")
	cfg, wrote, created, err := InitConfig(path, "ws://127.0.0.1:8080", "", "test-secret", false)
	if err != nil {
		t.Fatalf("InitConfig: %v", err)
	}
	if !created {
		t.Fatal("expected created=true")
	}
	if wrote != path {
		t.Fatalf("path=%q", wrote)
	}
	if cfg.MachineID == "" || cfg.Key == "" || cfg.JoinToken == "" {
		t.Fatal("expected generated credentials")
	}

	loaded, err := LoadConfig(path, "", "", "", "")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if loaded.MachineID != cfg.MachineID || loaded.Key != cfg.Key || loaded.JoinToken != cfg.JoinToken {
		t.Fatal("loaded config mismatch")
	}

	again, _, createdAgain, err := InitConfig(path, "", "", "test-secret", false)
	if err != nil {
		t.Fatalf("idempotent InitConfig: %v", err)
	}
	if createdAgain {
		t.Fatal("expected created=false for existing config")
	}
	if again.MachineID != cfg.MachineID {
		t.Fatal("idempotent init changed machine id")
	}

	forced, _, createdForced, err := InitConfig(path, "ws://127.0.0.1:8080", "", "test-secret", true)
	if err != nil {
		t.Fatalf("force InitConfig: %v", err)
	}
	if !createdForced {
		t.Fatal("expected created=true with force")
	}
	if forced.MachineID == cfg.MachineID {
		t.Fatal("force should regenerate machine id")
	}
}

func TestValidateCWD(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "project")
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{CWDRoot: root}
	if err := cfg.ValidateCWD(""); err != nil {
		t.Fatal(err)
	}
	if err := cfg.ValidateCWD("relative"); err == nil {
		t.Fatal("expected relative cwd error")
	}
	if err := cfg.ValidateCWD(inside); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := cfg.ValidateCWD(outside); err == nil {
		t.Fatal("expected outside root error")
	}
}

func TestValidateCWDRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	escape := filepath.Join(root, "escape")
	if err := os.Symlink(outside, escape); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	cfg := &Config{CWDRoot: root}
	if err := cfg.ValidateCWD(escape); err == nil {
		t.Fatal("expected symlink escape to be rejected")
	}
}
