package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/folsomintel/kuma/internal/cli"
)

// version is injected by GoReleaser via -X main.version={{.Version}}.
var version = "dev"

func main() {
	if err := newRootCmd().Execute(); err != nil {
		if errors.Is(err, cli.ErrCanceled) {
			os.Exit(130)
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
