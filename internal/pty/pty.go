package pty

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	creackpty "github.com/creack/pty"
)

// Session is a process attached to a pseudo-terminal.
type Session struct {
	cmd  *exec.Cmd
	ptmx *os.File

	mu       sync.Mutex
	closed   bool
	waitCh   chan error
	waitOnce sync.Once
}

// Start launches command with args in a new PTY.
// cwd may be empty to inherit the daemon working directory.
// The child environment is a filtered copy of the process environment
// with KUMA_* variables removed so agents cannot read daemon secrets.
func Start(command string, args []string, cwd string) (*Session, error) {
	if command == "" {
		return nil, errors.New("command is required")
	}

	cmd := exec.Command(command, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Env = filteredEnviron(os.Environ())

	ptmx, err := creackpty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("start pty: %w", err)
	}

	s := &Session{
		cmd:    cmd,
		ptmx:   ptmx,
		waitCh: make(chan error, 1),
	}
	go s.reap()
	return s, nil
}

// filteredEnviron returns env without KUMA_* variables.
func filteredEnviron(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		key, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(key, "KUMA_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func (s *Session) reap() {
	err := s.cmd.Wait()
	s.waitOnce.Do(func() {
		s.waitCh <- err
		close(s.waitCh)
	})
}

// Read implements io.Reader.
func (s *Session) Read(p []byte) (int, error) {
	return s.ptmx.Read(p)
}

// Write implements io.Writer.
func (s *Session) Write(p []byte) (int, error) {
	return s.ptmx.Write(p)
}

// Resize sets the remote terminal size.
func (s *Session) Resize(rows, cols uint16) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	return creackpty.Setsize(s.ptmx, &creackpty.Winsize{
		Rows: rows,
		Cols: cols,
	})
}

// Wait blocks until the process exits and returns its wait error.
func (s *Session) Wait() error {
	return <-s.waitCh
}

// ExitCode returns the process exit code after Wait, or -1 if unavailable.
func (s *Session) ExitCode() int {
	if s.cmd.ProcessState != nil {
		return s.cmd.ProcessState.ExitCode()
	}
	return -1
}

// Close kills the process if still running and closes the PTY. Idempotent.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true

	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	return s.ptmx.Close()
}
