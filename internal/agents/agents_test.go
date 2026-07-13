package agents_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/folsomintel/kuma/internal/agents"
)

func TestAllowed(t *testing.T) {
	if !agents.Allowed("claude") {
		t.Fatal("claude should be allowed")
	}
	if agents.Allowed("bash") {
		t.Fatal("bash should not be allowed")
	}
	if agents.Allowed("") {
		t.Fatal("empty should not be allowed")
	}
}

func TestLookupRejectsBasenameMismatch(t *testing.T) {
	dir := t.TempDir()
	// Create an executable whose basename is not an allowlisted agent name.
	evil := filepath.Join(dir, "evil-bin")
	if err := os.WriteFile(evil, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Place a PATH entry where LookPath("claude") finds a file named something else
	// via a directory that doesn't contain claude — Lookup should still fail for
	// non-allowlisted names, and Allowed gates Lookup.
	if path, ok := agents.Lookup("not-an-agent"); ok || path != "" {
		t.Fatalf("Lookup(not-an-agent)=%q ok=%v", path, ok)
	}
}

func TestDetectAndLookupWithAllowlistedBinary(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho claude\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	list := agents.Detect()
	var found bool
	for _, a := range list {
		if a.Name == "claude" {
			found = true
			if !a.Installed {
				t.Fatal("expected claude installed")
			}
			if a.Path != bin {
				t.Fatalf("path=%q want %q", a.Path, bin)
			}
		}
	}
	if !found {
		t.Fatal("claude missing from Detect")
	}

	path, ok := agents.Lookup("claude")
	if !ok || path != bin {
		t.Fatalf("Lookup(claude)=%q ok=%v", path, ok)
	}
}

func TestLookupAcceptsSymlinkWithMatchingBase(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real-evil")
	if err := os.WriteFile(target, []byte("#!/bin/sh\necho evil\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	link := filepath.Join(dir, "claude")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	path, ok := agents.Lookup("claude")
	if !ok {
		t.Fatal("expected symlink named claude to be accepted (basename matches)")
	}
	if filepath.Base(path) != "claude" {
		t.Fatalf("base=%q", filepath.Base(path))
	}
}
