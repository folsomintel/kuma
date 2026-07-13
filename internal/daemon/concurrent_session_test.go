package daemon

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"kuma/internal/crypto"
	"kuma/internal/protocol"
)

func TestConcurrentStartSessionRespectsMaxSessions(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	script := filepath.Join(binDir, "claude")
	// Long-lived agent so sessions stay in the map for the assertion.
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec sleep 3600\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	// Discarding WebSocket peer so send() succeeds during concurrent RPCs.
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(ts.Close)

	wsURL := "ws" + ts.URL[len("http"):]
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	const maxSessions = 2
	cfg := &Config{
		MachineID:    "concurrent-cap",
		Key:          crypto.EncodeKey(key),
		RelayURL:     "ws://127.0.0.1:1",
		JoinToken:    "test-token",
		HistoryLimit: 1024,
		MaxSessions:  maxSessions,
		MinBackoff:   time.Millisecond,
		MaxBackoff:   time.Millisecond,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	d, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	d.setConn(conn)
	t.Cleanup(d.closeAllSessions)

	params, err := protocol.EncodeParams(protocol.StartSessionParams{Agent: "claude"})
	if err != nil {
		t.Fatal(err)
	}

	const storm = 24
	var wg sync.WaitGroup
	for i := 0; i < storm; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			msg := protocol.Message{
				Type:   protocol.TypeRequest,
				Seq:    1, // unused by handleRequest
				ID:     strconv.Itoa(i),
				Method: protocol.MethodStartSession,
				Params: params,
			}
			_ = d.handleRequest(msg)
		}(i)
	}
	wg.Wait()

	d.sessionsMu.Lock()
	n := len(d.sessions)
	reserved := d.reserved
	d.sessionsMu.Unlock()

	if reserved != 0 {
		t.Fatalf("reserved=%d after storm, want 0", reserved)
	}
	if n > maxSessions {
		t.Fatalf("sessions=%d exceeds MaxSessions=%d", n, maxSessions)
	}
	if n != maxSessions {
		t.Fatalf("sessions=%d, want exactly MaxSessions=%d", n, maxSessions)
	}
}
