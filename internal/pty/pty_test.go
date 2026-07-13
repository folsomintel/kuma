package pty_test

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"kuma/internal/pty"
)

func TestStartWriteReadClose(t *testing.T) {
	s, err := pty.Start("/bin/cat", nil, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := s.Resize(24, 80); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if _, err := s.Write([]byte("ping\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	buf := make([]byte, 64)
	deadline := time.Now().Add(2 * time.Second)
	var got string
	for time.Now().Before(deadline) {
		n, err := s.Read(buf)
		if n > 0 {
			got += string(buf[:n])
			if len(got) > 0 {
				break
			}
		}
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
	}
	if got == "" {
		t.Fatal("expected output from cat")
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestStartStripsKumaEnv(t *testing.T) {
	t.Setenv("KUMA_KEY", "super-secret-key")
	t.Setenv("KUMA_JOIN_TOKEN", "super-secret-token")
	t.Setenv("PATH", os.Getenv("PATH"))

	s, err := pty.Start("/usr/bin/env", nil, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Close()

	var out bytes.Buffer
	buf := make([]byte, 4096)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		n, err := s.Read(buf)
		if n > 0 {
			out.Write(buf[:n])
		}
		if err != nil {
			break
		}
		// env exits quickly; keep reading until EOF or timeout
		if out.Len() > 0 && n == 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}
	_ = s.Wait()

	envOut := out.String()
	if strings.Contains(envOut, "KUMA_KEY=") {
		t.Fatal("child env still contains KUMA_KEY")
	}
	if strings.Contains(envOut, "KUMA_JOIN_TOKEN=") {
		t.Fatal("child env still contains KUMA_JOIN_TOKEN")
	}
	if !strings.Contains(envOut, "PATH=") {
		t.Fatal("expected PATH to be preserved in child env")
	}
}
