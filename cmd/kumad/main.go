package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"kuma/internal/daemon"
	"kuma/internal/jointoken"
)

func main() {
	var (
		configPath = flag.String("config", "", "path to config JSON (default: user config dir /kuma/config.json)")
		machineID  = flag.String("machine-id", "", "machine id (overrides config/env)")
		key        = flag.String("key", "", "E2E AES-256 key, base64url (overrides config/env)")
		relayURL   = flag.String("relay-url", "", "relay WebSocket base URL (overrides config/env)")
		joinToken  = flag.String("join-token", "", "relay join token (overrides config/env)")
		authSecret = flag.String("auth-secret", "", "relay auth secret used by init to mint join token (or KUMA_RELAY_AUTH_SECRET)")
		force      = flag.Bool("force", false, "overwrite existing config when running init")
	)
	flag.Parse()

	if flag.NArg() > 0 {
		switch flag.Arg(0) {
		case "init":
			if err := runInit(*configPath, *relayURL, *joinToken, *authSecret, *force); err != nil {
				fmt.Fprintf(os.Stderr, "init error: %v\n", err)
				os.Exit(1)
			}
			return
		case "mint-token":
			if err := runMintToken(*machineID, *authSecret, flag.Args()[1:]); err != nil {
				fmt.Fprintf(os.Stderr, "mint-token error: %v\n", err)
				os.Exit(1)
			}
			return
		default:
			fmt.Fprintf(os.Stderr, "unknown command %q (available: init, mint-token)\n", flag.Arg(0))
			os.Exit(2)
		}
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := daemon.LoadConfig(*configPath, *machineID, *key, *relayURL, *joinToken)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		fmt.Fprintf(os.Stderr, "hint: run `just init` or `go run ./cmd/kumad init` to create a local config\n")
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Info("starting kumad", "machine_id", cfg.MachineID, "relay_url", cfg.RelayURL)
	d, err := daemon.New(cfg, log)
	if err != nil {
		fmt.Fprintf(os.Stderr, "daemon error: %v\n", err)
		os.Exit(1)
	}
	if err := d.Run(ctx); err != nil && err != context.Canceled {
		log.Error("kumad stopped", "err", err)
		os.Exit(1)
	}
}

func runInit(configPath, relayURL, joinToken, authSecret string, force bool) error {
	if authSecret == "" {
		authSecret = os.Getenv("KUMA_RELAY_AUTH_SECRET")
	}
	if joinToken == "" {
		joinToken = os.Getenv("KUMA_JOIN_TOKEN")
	}
	cfg, path, created, err := daemon.InitConfig(configPath, relayURL, joinToken, authSecret, force)
	if err != nil {
		return err
	}
	if created {
		fmt.Printf("wrote %s\n", path)
	} else {
		fmt.Printf("using existing %s\n", path)
		fmt.Println("(pass -force to regenerate)")
	}
	fmt.Printf("machine_id=%s\n", cfg.MachineID)
	fmt.Printf("relay_url=%s\n", cfg.RelayURL)
	fmt.Println("warning: treat key= as a secret; avoid shell history / logs")
	fmt.Printf("key=%s\n", cfg.Key)
	fmt.Printf("join_token=%s\n", cfg.JoinToken)
	fmt.Println("next: start the relay (`just relay`), then run `just kumad`")
	return nil
}

func runMintToken(machineID, authSecret string, args []string) error {
	fs := flag.NewFlagSet("mint-token", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	role := fs.String("role", jointoken.RoleClient, "join role: client or daemon")
	mid := fs.String("machine-id", machineID, "machine id")
	secret := fs.String("auth-secret", authSecret, "relay auth secret (or KUMA_RELAY_AUTH_SECRET)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *mid == "" {
		*mid = os.Getenv("KUMA_MACHINE_ID")
	}
	if *secret == "" {
		*secret = os.Getenv("KUMA_RELAY_AUTH_SECRET")
	}
	*role = strings.TrimSpace(*role)
	tok, err := jointoken.Mint(*secret, *mid, *role)
	if err != nil {
		return err
	}
	fmt.Println(tok)
	return nil
}
