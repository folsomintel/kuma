package wsutil

import "time"

// Shared WebSocket pump timings for kumad and kuma-relay.
const (
	WriteWait      = 10 * time.Second
	PongWait       = 60 * time.Second
	PingPeriod     = 30 * time.Second
	MaxMessageSize = 1 << 20 // 1 MiB
)
