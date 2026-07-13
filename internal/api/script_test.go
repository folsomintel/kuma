package api

import (
	"strings"
	"testing"
)

func TestBuildKumadStartupScript(t *testing.T) {
	sha := strings.Repeat("a", 64)
	script := buildKumadStartupScript("https://example.com/kumad", sha, "m1", "k1", "join1", "ws://relay")
	if !strings.Contains(script, "m1") || !strings.Contains(script, "k1") || !strings.Contains(script, "join1") {
		t.Fatalf("script missing credentials: %s", script)
	}
	if !strings.Contains(script, "https://example.com/kumad") {
		t.Fatal("script missing download url")
	}
	if !strings.Contains(script, "sha256sum -c") {
		t.Fatal("script missing checksum verify")
	}
	if !strings.Contains(script, "join_token") {
		t.Fatal("script missing join_token in config")
	}
	if !strings.Contains(script, "exec /usr/local/bin/kumad") {
		t.Fatal("script missing kumad exec")
	}
}

func TestBuildKumadStartupScriptNoDownload(t *testing.T) {
	script := buildKumadStartupScript("", "", "m1", "k1", "join1", "ws://relay")
	if strings.Contains(script, "curl") {
		t.Fatal("unexpected curl without download url")
	}
	if !strings.Contains(script, "command -v kumad") {
		t.Fatal("expected baked-binary path")
	}
}
