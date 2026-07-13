package connect

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"
)

// Terminal wraps a local TTY for raw-mode I/O and resize notifications.
type Terminal struct {
	fd       int
	oldState *term.State
}

// NewTerminal puts stdin into raw mode when it is a TTY.
func NewTerminal() (*Terminal, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return &Terminal{fd: fd}, nil
	}
	old, err := term.MakeRaw(fd)
	if err != nil {
		return nil, fmt.Errorf("make raw: %w", err)
	}
	return &Terminal{fd: fd, oldState: old}, nil
}

// Restore returns the terminal to its previous mode.
func (t *Terminal) Restore() error {
	if t == nil || t.oldState == nil {
		return nil
	}
	err := term.Restore(t.fd, t.oldState)
	t.oldState = nil
	return err
}

// Size returns the current terminal size, or 24x80 when not a TTY.
func (t *Terminal) Size() (rows, cols uint16, err error) {
	if t == nil || !term.IsTerminal(t.fd) {
		return 24, 80, nil
	}
	w, h, err := term.GetSize(t.fd)
	if err != nil {
		return 0, 0, err
	}
	if h <= 0 || w <= 0 {
		return 24, 80, nil
	}
	if h > 0xffff {
		h = 0xffff
	}
	if w > 0xffff {
		w = 0xffff
	}
	return uint16(h), uint16(w), nil
}

// IsRaw reports whether the terminal was put into raw mode.
func (t *Terminal) IsRaw() bool {
	return t != nil && t.oldState != nil
}

// WatchResize delivers terminal size updates on SIGWINCH until ctxDone closes.
// When fd is not a TTY, the returned channel never receives.
func WatchResize(fd int, ctxDone <-chan struct{}) <-chan struct{} {
	ch := make(chan struct{}, 1)
	if !term.IsTerminal(fd) {
		return ch
	}
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	go func() {
		defer signal.Stop(sigCh)
		defer close(ch)
		for {
			select {
			case <-ctxDone:
				return
			case <-sigCh:
				select {
				case ch <- struct{}{}:
				default:
				}
			}
		}
	}()
	return ch
}
