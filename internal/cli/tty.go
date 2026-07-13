package cli

import (
	"fmt"
	"os"

	"golang.org/x/term"
)

// IsTTY reports whether stdin is an interactive terminal.
func IsTTY() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// RequireTTY returns an error when stdin is not a TTY.
func RequireTTY(action string) error {
	if IsTTY() {
		return nil
	}
	return fmt.Errorf("%s requires an interactive terminal; pass explicit args or flags", action)
}
