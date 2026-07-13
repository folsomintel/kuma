package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/folsomintel/kuma/internal/api"
	"github.com/folsomintel/kuma/internal/fuse"
	"github.com/folsomintel/kuma/internal/store"
)

func main() {
	var (
		addr        = flag.String("addr", "", "listen address (default KUMA_API_ADDR or :8090)")
		apiToken    = flag.String("api-token", "", "bearer token (or KUMA_API_TOKEN)")
		relayURL    = flag.String("relay-url", "", "public relay ws URL (or KUMA_RELAY_URL)")
		relaySecret = flag.String("relay-auth-secret", "", "shared relay join secret (or KUMA_RELAY_AUTH_SECRET)")
		fuseURL     = flag.String("fuse-url", "", "Fuse orchestrator URL (or FUSE_BASE_URL)")
		fuseTok     = flag.String("fuse-token", "", "Fuse bearer token (or FUSE_TOKEN)")
		kumadURL    = flag.String("kumad-download-url", "", "URL to fetch kumad into Fuse guests (or KUMAD_DOWNLOAD_URL)")
		kumadSHA    = flag.String("kumad-download-sha256", "", "sha256 of kumad binary (or KUMAD_DOWNLOAD_SHA256)")
	)
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := api.LoadConfig(*addr, *apiToken, *relayURL, *relaySecret, *fuseURL, *fuseTok, *kumadURL, *kumadSHA)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	fuseClient, err := fuse.New(cfg.FuseBaseURL, cfg.FuseToken)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fuse client error: %v\n", err)
		os.Exit(1)
	}

	srv := api.NewServer(cfg, store.NewMemory(), fuseClient, log)
	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	log.Info("starting kuma-api",
		"addr", cfg.Addr,
		"relay_url", cfg.RelayURL,
		"fuse_url", cfg.FuseBaseURL,
	)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "listen error: %v\n", err)
		os.Exit(1)
	}
}
