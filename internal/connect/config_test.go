package connect_test

import (
	"path/filepath"
	"testing"

	"github.com/folsomintel/kuma/internal/connect"
	"github.com/folsomintel/kuma/internal/crypto"
)

func TestRemotesAddListRemove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remotes.json")
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	remote := connect.Remote{
		MachineID: "m1",
		Key:       crypto.EncodeKey(key),
		RelayURL:  "ws://127.0.0.1:8080",
		JoinToken: "tok",
	}
	if err := connect.AddRemote(path, "mac", remote, true); err != nil {
		t.Fatal(err)
	}

	f, err := connect.LoadRemotesFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if f.Default != "mac" {
		t.Fatalf("default=%q", f.Default)
	}
	if f.Remotes["mac"].MachineID != "m1" {
		t.Fatalf("remote=%+v", f.Remotes["mac"])
	}

	cred, err := connect.ResolveCredentials(path, "mac", "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if cred.MachineID != "m1" || cred.JoinToken != "tok" {
		t.Fatalf("cred=%+v", cred)
	}

	if err := connect.RemoveRemote(path, "mac"); err != nil {
		t.Fatal(err)
	}
	f, err = connect.LoadRemotesFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Remotes) != 0 {
		t.Fatalf("expected empty, got %+v", f.Remotes)
	}
}

func TestResolveCredentialsFlagsOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remotes.json")
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	encoded := crypto.EncodeKey(key)
	if err := connect.AddRemote(path, "mac", connect.Remote{
		MachineID: "file-m",
		Key:       encoded,
		RelayURL:  "ws://file",
		JoinToken: "file-tok",
	}, true); err != nil {
		t.Fatal(err)
	}

	cred, err := connect.ResolveCredentials(path, "mac", "flag-m", "", "ws://flag", "flag-tok")
	if err != nil {
		t.Fatal(err)
	}
	if cred.MachineID != "flag-m" || cred.RelayURL != "ws://flag" || cred.JoinToken != "flag-tok" {
		t.Fatalf("cred=%+v", cred)
	}
	if string(cred.KeyBytes()) != string(key) {
		t.Fatal("key bytes mismatch")
	}
}

func TestResolveCredentialsRequiresSomething(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")
	_, err := connect.ResolveCredentials(path, "", "", "", "", "")
	if err == nil {
		t.Fatal("expected error")
	}
}
