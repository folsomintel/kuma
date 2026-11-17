package connect_test

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/folsomintel/kuma/internal/connect"
	"github.com/folsomintel/kuma/internal/crypto"
	"github.com/folsomintel/kuma/internal/daemon"
	"github.com/folsomintel/kuma/internal/jointoken"
	"github.com/folsomintel/kuma/internal/protocol"
	"github.com/folsomintel/kuma/internal/relay"
)

const connectE2ESecret = "connect-e2e-secret"

// startStack runs a relay and a daemon whose only agent is a fake "claude"
// that echoes its input, and returns credentials for dialing that daemon.
func startStack(t *testing.T, machineID string) *connect.Credentials {
	t.Helper()
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
	t.Cleanup(cancel)

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
	return cred
}

// dialReady connects a new client and waits for its tunnel, the way every
// kuma CLI invocation does. The relay's client slot is briefly still taken
// right after a previous client hangs up, so the dial is retried.
func dialReady(t *testing.T, cred *connect.Credentials) *connect.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		client, err := connect.Dial(ctx, cred)
		if err == nil {
			t.Cleanup(func() { _ = client.Close() })
			if _, err := client.WaitReady(ctx); err != nil {
				t.Fatalf("WaitReady: %v", err)
			}
			return client
		}
		if ctx.Err() != nil {
			t.Fatalf("Dial: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestClientStartSessionRoundTrip(t *testing.T) {
	cred := startStack(t, "connect-e2e")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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

// Every CLI invocation is a new client on the daemon's one long-lived relay
// connection. Each must get a working tunnel, whatever the previous client
// sent and however much output the daemon produced that nobody received.
func TestSuccessiveClientsOnOneDaemonConnection(t *testing.T) {
	cred := startStack(t, "connect-successive")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	first := dialReady(t, cred)
	start, err := first.StartSession(ctx, "claude", "")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	// Leave the agent producing output with no client attached.
	for range 100 {
		if err := first.SendInput(start.SessionID, []byte("unread-line\n")); err != nil {
			t.Fatalf("SendInput: %v", err)
		}
	}
	_ = first.Close()

	second := dialReady(t, cred)
	list, err := second.ListSessions(ctx)
	if err != nil {
		t.Fatalf("second client ListSessions: %v", err)
	}
	if len(list.Sessions) != 1 || list.Sessions[0].SessionID != start.SessionID {
		t.Fatalf("second client sees sessions %+v, want %s", list.Sessions, start.SessionID)
	}
	hist, err := second.GetHistory(ctx, start.SessionID)
	if err != nil {
		t.Fatalf("second client GetHistory: %v", err)
	}
	if !strings.Contains(string(hist.History), "unread-line") {
		t.Fatalf("history missing agent output: %q", hist.History)
	}
	_ = second.Close()

	third := dialReady(t, cred)
	if err := third.StopSession(ctx, start.SessionID); err != nil {
		t.Fatalf("third client StopSession: %v", err)
	}
}
