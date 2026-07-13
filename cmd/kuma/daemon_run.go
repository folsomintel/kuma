package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/folsomintel/kuma/internal/daemon"
)

func runDaemonForeground(rf *rootFlags) error {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := daemon.LoadConfig(rf.configPath, rf.machineID, rf.key, rf.relayURL, rf.joinToken)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Info("starting kuma daemon", "machine_id", cfg.MachineID, "relay_url", cfg.RelayURL)
	d, err := daemon.New(cfg, log)
	if err != nil {
		return err
	}
	if err := d.Run(ctx); err != nil && err != context.Canceled {
		return err
	}
	return nil
}
