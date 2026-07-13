package relay

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"kuma/internal/jointoken"
)

const testSecret = "relay-test-secret"

func testServer(t *testing.T, opts Options) (*Server, *httptest.Server) {
	t.Helper()
	if opts.AuthSecret == "" {
		opts.AuthSecret = testSecret
	}
	srv := NewServer(opts)
	t.Cleanup(srv.Close)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts
}

func wsURL(ts *httptest.Server, machineID, role string) string {
	tok, err := jointoken.Mint(testSecret, machineID, role)
	if err != nil {
		panic(err)
	}
	base := "ws" + strings.TrimPrefix(ts.URL, "http")
	return base + "/ws/" + machineID + "/" + role + "?token=" + tok
}

func TestHealthAndReady(t *testing.T) {
	_, ts := testServer(t, Options{})

	for _, path := range []string{"/healthz", "/readyz"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK || string(body) != "ok" {
			t.Fatalf("%s: status=%d body=%q", path, resp.StatusCode, body)
		}
	}
}

func TestInvalidWSPaths(t *testing.T) {
	_, ts := testServer(t, Options{})

	cases := []string{
		"/ws/",
		"/ws/only",
		"/ws/machine/badrole",
		"/ws/a/b/c",
	}
	for _, path := range cases {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusSwitchingProtocols {
			t.Fatalf("%s unexpectedly upgraded", path)
		}
	}
}

func TestRejectsMissingToken(t *testing.T) {
	_, ts := testServer(t, Options{})
	wsBase := "ws" + strings.TrimPrefix(ts.URL, "http")
	_, resp, err := websocket.DefaultDialer.Dial(wsBase+"/ws/m1/daemon", nil)
	if err == nil {
		t.Fatal("expected dial failure")
	}
	if resp != nil && resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestOpaqueBinaryForwarding(t *testing.T) {
	_, ts := testServer(t, Options{})

	daemon, _, err := websocket.DefaultDialer.Dial(wsURL(ts, "m1", jointoken.RoleDaemon), nil)
	if err != nil {
		t.Fatalf("dial daemon: %v", err)
	}
	t.Cleanup(func() { _ = daemon.Close() })

	client, _, err := websocket.DefaultDialer.Dial(wsURL(ts, "m1", jointoken.RoleClient), nil)
	if err != nil {
		t.Fatalf("dial client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	payload := []byte{0x00, 0x01, 0x02, 0xff, 'x'}
	if err := client.WriteMessage(websocket.BinaryMessage, payload); err != nil {
		t.Fatalf("client write: %v", err)
	}

	_ = daemon.SetReadDeadline(time.Now().Add(2 * time.Second))
	msgType, got, err := daemon.ReadMessage()
	if err != nil {
		t.Fatalf("daemon read: %v", err)
	}
	if msgType != websocket.BinaryMessage {
		t.Fatalf("got type %d", msgType)
	}
	if string(got) != string(payload) {
		t.Fatalf("got %v want %v", got, payload)
	}

	reply := []byte("opaque-reply")
	if err := daemon.WriteMessage(websocket.BinaryMessage, reply); err != nil {
		t.Fatalf("daemon write: %v", err)
	}
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	msgType, got, err = client.ReadMessage()
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if msgType != websocket.BinaryMessage || string(got) != string(reply) {
		t.Fatalf("unexpected reply type=%d data=%v", msgType, got)
	}
}

func TestDropsTextFrames(t *testing.T) {
	_, ts := testServer(t, Options{})

	daemon, _, err := websocket.DefaultDialer.Dial(wsURL(ts, "m2", jointoken.RoleDaemon), nil)
	if err != nil {
		t.Fatalf("dial daemon: %v", err)
	}
	t.Cleanup(func() { _ = daemon.Close() })

	client, _, err := websocket.DefaultDialer.Dial(wsURL(ts, "m2", jointoken.RoleClient), nil)
	if err != nil {
		t.Fatalf("dial client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	if err := client.WriteMessage(websocket.TextMessage, []byte("not-binary")); err != nil {
		t.Fatalf("write text: %v", err)
	}
	if err := client.WriteMessage(websocket.BinaryMessage, []byte("ok")); err != nil {
		t.Fatalf("write binary: %v", err)
	}

	_ = daemon.SetReadDeadline(time.Now().Add(2 * time.Second))
	msgType, got, err := daemon.ReadMessage()
	if err != nil {
		t.Fatalf("daemon read: %v", err)
	}
	if msgType != websocket.BinaryMessage || string(got) != "ok" {
		t.Fatalf("expected binary ok, got type=%d data=%q", msgType, got)
	}
}

func TestRejectDuplicateSameRole(t *testing.T) {
	_, ts := testServer(t, Options{})

	first, _, err := websocket.DefaultDialer.Dial(wsURL(ts, "m3", jointoken.RoleDaemon), nil)
	if err != nil {
		t.Fatalf("dial first: %v", err)
	}
	t.Cleanup(func() { _ = first.Close() })

	_, resp, err := websocket.DefaultDialer.Dial(wsURL(ts, "m3", jointoken.RoleDaemon), nil)
	if err == nil {
		t.Fatal("expected second dial to fail")
	}
	if resp != nil && resp.StatusCode != http.StatusConflict {
		t.Fatalf("status=%d want 409", resp.StatusCode)
	}

	// First connection should still be alive.
	if err := first.WriteMessage(websocket.BinaryMessage, []byte("still-here")); err != nil {
		t.Fatalf("first write: %v", err)
	}
}

func TestDropWhenPeerMissing(t *testing.T) {
	_, ts := testServer(t, Options{})

	client, _, err := websocket.DefaultDialer.Dial(wsURL(ts, "m4", jointoken.RoleClient), nil)
	if err != nil {
		t.Fatalf("dial client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	if err := client.WriteMessage(websocket.BinaryMessage, []byte("lonely")); err != nil {
		t.Fatalf("write: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
}

func TestParseWSPath(t *testing.T) {
	id, role, ok := parseWSPath("/ws/abc/daemon")
	if !ok || id != "abc" || role != "daemon" {
		t.Fatalf("got %q %q %v", id, role, ok)
	}
	if _, _, ok := parseWSPath("/ws/abc/other"); ok {
		t.Fatal("expected failure")
	}
}

func TestMaxRooms(t *testing.T) {
	_, ts := testServer(t, Options{MaxRooms: 1})

	first, _, err := websocket.DefaultDialer.Dial(wsURL(ts, "room1", jointoken.RoleDaemon), nil)
	if err != nil {
		t.Fatalf("dial first: %v", err)
	}
	t.Cleanup(func() { _ = first.Close() })

	_, resp, err := websocket.DefaultDialer.Dial(wsURL(ts, "room2", jointoken.RoleDaemon), nil)
	if err == nil {
		t.Fatal("expected max rooms rejection")
	}
	if resp != nil && resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestDisconnectsSlowPeer(t *testing.T) {
	_, ts := testServer(t, Options{})

	daemon, _, err := websocket.DefaultDialer.Dial(wsURL(ts, "slow", jointoken.RoleDaemon), nil)
	if err != nil {
		t.Fatalf("dial daemon: %v", err)
	}
	t.Cleanup(func() { _ = daemon.Close() })

	client, _, err := websocket.DefaultDialer.Dial(wsURL(ts, "slow", jointoken.RoleClient), nil)
	if err != nil {
		t.Fatalf("dial client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	// Flood the daemon without reading so its send buffer fills (capacity 32).
	payload := bytes.Repeat([]byte("x"), 64)
	for i := 0; i < 64; i++ {
		if err := client.WriteMessage(websocket.BinaryMessage, payload); err != nil {
			break
		}
	}

	_ = daemon.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = daemon.ReadMessage()
	if err == nil {
		// Drain any buffered frames, then expect close.
		for {
			_ = daemon.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			_, _, err = daemon.ReadMessage()
			if err != nil {
				break
			}
		}
	}
	if err == nil {
		t.Fatal("expected daemon connection to close under backpressure")
	}
}
