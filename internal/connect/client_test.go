package connect_test

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kuma/internal/connect"
	"kuma/internal/crypto"
	"kuma/internal/daemon"
	"kuma/internal/jointoken"
	"kuma/internal/protocol"
	"kuma/internal/relay"
)

const connectE2ESecret = "connect-e2e-secret"

func TestClientStartSessionRoundTrip(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	script := filepath.Join(binDir, "claude")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec cat\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	relaySrv := relay.NewServer(relay.Options{AuthSecret: connectE2ESecret})
	t.Cleanup(relaySrv.Close)
	ts := httptest.NewServer(relaySrv.Handler())
	t.Cleanup(ts.Close)

	machineID := "connect-e2e"
	daemonTok, err := jointoken.Mint(connectE2ESecret, machineID, jointoken.RoleDaemon)
	if err != nil {
		t.Fatal(err)
	}
	clientTok, err := jointoken.Mint(connectE2ESecret, machineID, jointoken.RoleClient)
	if err != nil {
		t.Fatal(err)
	}

	wsBase := "ws" + strings.TrimPrefix(ts.URL, "http")
	cfg := &daemon.Config{
		MachineID:    machineID,
		Key:          crypto.EncodeKey(key),
		RelayURL:     wsBase,
		JoinToken:    daemonTok,
		HistoryLimit: 64 * 1024,
		MaxSessions:  8,
		MinBackoff:   50 * time.Millisecond,
		MaxBackoff:   200 * time.Millisecond,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d, err := daemon.New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = d.Run(ctx) }()

	cred := &connect.Credentials{
		MachineID: machineID,
		Key:       crypto.EncodeKey(key),
		RelayURL:  wsBase,
		JoinToken: clientTok,
	}
	if err := cred.Validate(); err != nil {
		t.Fatal(err)
	}

	client, err := connect.Dial(ctx, cred)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	readyCtx, readyCancel := context.WithTimeout(ctx, 10*time.Second)
	defer readyCancel()
	agents, err := client.WaitReady(readyCtx)
	if err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
	found := false
	for _, a := range agents.Agents {
		if a.Name == "claude" && a.Installed {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("claude not installed in agents: %+v", agents.Agents)
	}

	start, err := client.StartSession(ctx, "claude", "")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if start.SessionID == "" {
		t.Fatal("empty session id")
	}

	if err := client.SendInput(start.SessionID, []byte("hello-connect\n")); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	var out strings.Builder
	for time.Now().Before(deadline) {
		recvCtx, recvCancel := context.WithTimeout(ctx, time.Until(deadline))
		msg, err := client.Recv(recvCtx)
		recvCancel()
		if err != nil {
			continue
		}
		if msg.Type == protocol.TypeOutput && msg.SessionID == start.SessionID {
			out.Write(msg.Data)
			if strings.Contains(out.String(), "hello-connect") {
				break
			}
		}
	}
	if !strings.Contains(out.String(), "hello-connect") {
		t.Fatalf("expected echoed output, got %q", out.String())
	}

	if err := client.StopSession(ctx, start.SessionID); err != nil {
		t.Fatalf("StopSession: %v", err)
	}
}
