package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/folsomintel/kuma/internal/connect"
	"github.com/folsomintel/kuma/internal/protocol"
)

// CredFlags are explicit credential overrides from CLI flags.
type CredFlags struct {
	ConfigPath string
	Remote     string
	MachineID  string
	Key        string
	RelayURL   string
	JoinToken  string
}

// DialReady dials and waits until the remote daemon responds.
func DialReady(ctx context.Context, f CredFlags) (*connect.Client, protocol.ListAgentsResult, error) {
	cred, err := connect.ResolveCredentials(f.ConfigPath, f.Remote, f.MachineID, f.Key, f.RelayURL, f.JoinToken)
	if err != nil {
		return nil, protocol.ListAgentsResult{}, err
	}
	if IsTTY() {
		fmt.Fprintf(os.Stderr, "dialing relay %s…\n", cred.RelayURL)
	}
	client, err := connect.Dial(ctx, cred)
	if err != nil {
		return nil, protocol.ListAgentsResult{}, wrapDialError(err, cred.RelayURL)
	}
	if IsTTY() {
		fmt.Fprintf(os.Stderr, "waiting for daemon %s…\n", cred.MachineID)
	}
	readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	list, err := client.WaitReady(readyCtx)
	if err != nil {
		_ = client.Close()
		return nil, protocol.ListAgentsResult{}, fmt.Errorf("waiting for daemon: %w\nhint: is kumad up on that machine? local: kuma status / kuma up", err)
	}
	return client, list, nil
}

// ResolveRemoteName returns an explicit remote, else env/file default, else prompts.
func ResolveRemoteName(configPath, remote string) (string, error) {
	if strings.TrimSpace(remote) != "" {
		return strings.TrimSpace(remote), nil
	}
	if env := strings.TrimSpace(os.Getenv("KUMA_CONNECT_REMOTE")); env != "" {
		return env, nil
	}
	f, err := connect.LoadRemotesFile(configPath)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(f.Default) != "" {
		return strings.TrimSpace(f.Default), nil
	}
	names := RemoteNames(f)
	switch len(names) {
	case 0:
		return "", fmt.Errorf("no remotes configured; run: kuma remote add <name>")
	case 1:
		return names[0], nil
	}
	if err := RequireTTY("selecting a remote"); err != nil {
		return "", fmt.Errorf("no default remote; pass a remote name (or set KUMA_CONNECT_REMOTE)")
	}
	return SelectRemote(configPath)
}

func wrapDialError(err error, relayURL string) error {
	msg := err.Error()
	if strings.Contains(msg, "connection refused") || strings.Contains(msg, "connect: connection refused") {
		return fmt.Errorf("%w\nhint: relay is not reachable at %s — start it with: just relay", err, relayURL)
	}
	return err
}
