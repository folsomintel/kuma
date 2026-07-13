package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/folsomintel/kuma/internal/relay"
)

func main() {
	var (
		addr           = flag.String("addr", envOr("KUMA_RELAY_ADDR", ":8080"), "listen address")
		authSecret     = flag.String("auth-secret", "", "relay join auth secret (or KUMA_RELAY_AUTH_SECRET)")
		origins        = flag.String("allowed-origins", "", "comma-separated browser origins (empty = same-origin only)")
		maxRooms       = flag.Int("max-rooms", 0, "max concurrent rooms (default 10000)")
		maxConnsPerIP  = flag.Int("max-conns-per-ip", 0, "max concurrent WS connections per IP (default 32)")
		trustForwarded = flag.Bool("trust-forwarded-headers", false, "trust X-Forwarded-For for per-IP limits")
	)
	flag.Parse()

	secret := strings.TrimSpace(*authSecret)
	if secret == "" {
		secret = strings.TrimSpace(os.Getenv("KUMA_RELAY_AUTH_SECRET"))
	}
	if secret == "" {
		fmt.Fprintf(os.Stderr, "config error: relay auth secret is required (-auth-secret or KUMA_RELAY_AUTH_SECRET)\n")
		os.Exit(1)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	var allowed []string
	if *origins != "" {
		for _, o := range strings.Split(*origins, ",") {
			o = strings.TrimSpace(o)
			if o != "" {
				allowed = append(allowed, o)
			}
		}
	}

	srv := relay.NewServer(relay.Options{
		AuthSecret:            secret,
		AllowedOrigins:        allowed,
		MaxRooms:              *maxRooms,
		MaxConnsPerIP:         *maxConnsPerIP,
		TrustForwardedHeaders: *trustForwarded,
		Logger:                log,
	})
	defer srv.Close()

	if *trustForwarded {
		log.Warn("trust-forwarded-headers enabled; only use behind a reverse proxy that overwrites X-Forwarded-For")
	}
	httpServer := &http.Server{
		Addr:              *addr,
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

	log.Info("starting kuma-relay", "addr", *addr)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "listen error: %v\n", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
