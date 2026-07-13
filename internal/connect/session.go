package connect

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"kuma/internal/protocol"
)

const waitReadyTimeout = 30 * time.Second

// BridgeOptions configures an interactive session attach.
type BridgeOptions struct {
	SessionID     string
	ReplayHistory bool
}

// Bridge attaches the local TTY to a remote PTY session until exit or cancel.
// Returns the remote exit code when available (otherwise 0).
func Bridge(ctx context.Context, client *Client, opts BridgeOptions) (int, error) {
	if opts.SessionID == "" {
		return 1, fmt.Errorf("session id is required")
	}

	if opts.ReplayHistory {
		hist, err := client.GetHistory(ctx, opts.SessionID)
		if err != nil {
			return 1, fmt.Errorf("get_history: %w", err)
		}
		if len(hist.History) > 0 {
			if _, err := os.Stdout.Write(hist.History); err != nil {
				return 1, err
			}
		}
	}

	term, err := NewTerminal()
	if err != nil {
		return 1, err
	}
	defer func() { _ = term.Restore() }()

	bridgeCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	if rows, cols, err := term.Size(); err == nil {
		_ = client.SendResize(opts.SessionID, rows, cols)
	}

	resizeCh := WatchResize(int(os.Stdin.Fd()), bridgeCtx.Done())
	errCh := make(chan error, 2)
	exitCode := 0

	go func() {
		buf := make([]byte, 4096)
		for {
			n, readErr := os.Stdin.Read(buf)
			if n > 0 {
				if err := client.SendInput(opts.SessionID, append([]byte(nil), buf[:n]...)); err != nil {
					errCh <- err
					return
				}
			}
			if readErr != nil {
				if readErr == io.EOF {
					errCh <- nil
					return
				}
				errCh <- readErr
				return
			}
		}
	}()

	go func() {
		for {
			msg, err := client.Recv(bridgeCtx)
			if err != nil {
				errCh <- err
				return
			}
			switch msg.Type {
			case protocol.TypeOutput:
				if msg.SessionID != opts.SessionID {
					continue
				}
				if _, err := os.Stdout.Write(msg.Data); err != nil {
					errCh <- err
					return
				}
			case protocol.TypeSessionExit:
				if msg.SessionID != opts.SessionID {
					continue
				}
				if msg.ExitCode != nil {
					exitCode = *msg.ExitCode
				}
				errCh <- nil
				return
			}
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	for {
		select {
		case <-bridgeCtx.Done():
			return exitCode, bridgeCtx.Err()
		case <-sigCh:
			cancel()
			_ = term.Restore()
			return 130, nil
		case <-resizeCh:
			if rows, cols, err := term.Size(); err == nil {
				_ = client.SendResize(opts.SessionID, rows, cols)
			}
		case err := <-errCh:
			cancel()
			if err != nil && err != context.Canceled && err != io.EOF {
				return exitCode, err
			}
			return exitCode, nil
		}
	}
}

// RunSession starts a new agent session and bridges the TTY.
func RunSession(ctx context.Context, client *Client, agent, cwd string) (int, error) {
	if agent == "" {
		return 1, fmt.Errorf("agent is required (pass --agent)")
	}
	readyCtx, cancel := context.WithTimeout(ctx, waitReadyTimeout)
	defer cancel()
	if _, err := client.WaitReady(readyCtx); err != nil {
		return 1, err
	}
	start, err := client.StartSession(ctx, agent, cwd)
	if err != nil {
		return 1, fmt.Errorf("start_session: %w", err)
	}
	return Bridge(ctx, client, BridgeOptions{SessionID: start.SessionID})
}

// AttachSession reattaches to an existing session, replaying history first.
func AttachSession(ctx context.Context, client *Client, sessionID string) (int, error) {
	if sessionID == "" {
		return 1, fmt.Errorf("session id is required (pass --session)")
	}
	readyCtx, cancel := context.WithTimeout(ctx, waitReadyTimeout)
	defer cancel()
	if _, err := client.WaitReady(readyCtx); err != nil {
		return 1, err
	}
	return Bridge(ctx, client, BridgeOptions{SessionID: sessionID, ReplayHistory: true})
}
