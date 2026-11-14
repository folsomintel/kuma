package daemon_test

import (
	"context"
	"io"
	"net"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/folsomintel/kuma/internal/crypto"
	"github.com/folsomintel/kuma/internal/daemon"
	"github.com/folsomintel/kuma/internal/jointoken"
	"github.com/folsomintel/kuma/internal/protocol"
	"github.com/folsomintel/kuma/internal/relay"
	"github.com/folsomintel/kuma/internal/tunnel"
)

// cutProxy forwards TCP to a backend and can sever every connection on
// demand. The daemon dials the relay through it so a test can force a relay
// reconnect, which is what whoever operates the network path can do at will.
type cutProxy struct {
	ln    net.Listener
	mu    sync.Mutex
	conns []net.Conn
}

func newCutProxy(t *testing.T, backend string) *cutProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &cutProxy{ln: ln}
	t.Cleanup(func() {
		_ = ln.Close()
		p.cut()
	})
	go func() {
		for {
			in, err := ln.Accept()
			if err != nil {
				return
			}
			out, err := net.Dial("tcp", backend)
			if err != nil {
				_ = in.Close()
				continue
			}
			p.mu.Lock()
			p.conns = append(p.conns, in, out)
			p.mu.Unlock()
			go func() { _, _ = io.Copy(out, in); _ = out.Close() }()
			go func() { _, _ = io.Copy(in, out); _ = in.Close() }()
		}
	}()
	return p
}

func (p *cutProxy) cut() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.conns {
		_ = c.Close()
	}
	p.conns = nil
}

// dialRelay retries until the relay accepts the connection; the client slot is
// briefly still taken right after a previous client hangs up.
func dialRelay(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, _, err := websocket.DefaultDialer.Dial(url, nil)
		if err == nil {
			return conn
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial relay: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Someone who can record and re-send frames, but does not hold the machine
// key, must not be able to make the daemon act on a recorded request: not on
// the same relay connection, not after the daemon reconnects, and not by
// replaying the handshake that preceded it.
func TestReplayedFramesAreNeverExecuted(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	relaySrv := relay.NewServer(relay.Options{AuthSecret: e2eSecret})
	t.Cleanup(relaySrv.Close)
	ts := httptest.NewServer(relaySrv.Handler())
	t.Cleanup(ts.Close)
	proxy := newCutProxy(t, ts.Listener.Addr().String())

	machineID := "replay-machine"
	daemonTok, err := jointoken.Mint(e2eSecret, machineID, jointoken.RoleDaemon)
	if err != nil {
		t.Fatal(err)
	}
	clientTok, err := jointoken.Mint(e2eSecret, machineID, jointoken.RoleClient)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &daemon.Config{
		MachineID:  machineID,
		Key:        crypto.EncodeKey(key),
		RelayURL:   "ws://" + proxy.ln.Addr().String(),
		JoinToken:  daemonTok,
		MinBackoff: 20 * time.Millisecond,
		MaxBackoff: 50 * time.Millisecond,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	d, err := daemon.New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = d.Run(ctx) }()

	clientURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/" + machineID + "/client?token=" + clientTok

	// A legitimate client attaches and makes one request. Its hello and its
	// request are the frames an eavesdropper gets to keep.
	var victim *clientSession
	for deadline := time.Now().Add(10 * time.Second); ; {
		victim = &clientSession{conn: dialRelay(t, clientURL)}
		if err := victim.handshake(key, machineID); err == nil {
			break
		}
		_ = victim.conn.Close()
		if time.Now().After(deadline) {
			t.Fatal("daemon never completed a handshake")
		}
	}
	recordedHello := victim.hello
	recordedRequest, err := victim.channel.Seal(protocol.Message{
		Type:   protocol.TypeRequest,
		ID:     "recorded",
		Method: protocol.MethodListSessions,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := victim.conn.WriteMessage(websocket.BinaryMessage, recordedRequest); err != nil {
		t.Fatal(err)
	}
	if resp, err := victim.recv(t, 2*time.Second); err != nil || resp.ID != "recorded" {
		t.Fatalf("original request: resp=%+v err=%v", resp, err)
	}
	_ = victim.conn.Close()

	// The attacker takes the client slot and bounces the daemon's connection.
	proxy.cut()
	attacker := dialRelay(t, clientURL)
	t.Cleanup(func() { _ = attacker.Close() })
	frames := make(chan []byte, 256)
	go func() {
		defer close(frames)
		for {
			_, frame, err := attacker.ReadMessage()
			if err != nil {
				return
			}
			frames <- frame
		}
	}()
	write := func(frame []byte) {
		t.Helper()
		if err := attacker.WriteMessage(websocket.BinaryMessage, frame); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	// The daemon only ever sends data frames in reply to a request it
	// accepted (no agent sessions are running), so any data frame here means
	// a replay was executed.
	expectNoReply := func(wait time.Duration, what string) {
		t.Helper()
		timeout := time.After(wait)
		for {
			select {
			case frame, ok := <-frames:
				if !ok {
					t.Fatalf("relay connection closed while checking %s", what)
				}
				if tunnel.FrameKind(frame) == tunnel.KindData {
					t.Fatalf("daemon executed %s", what)
				}
			case <-timeout:
				return
			}
		}
	}
	// roundTrip proves the daemon is attached and still serves real clients.
	roundTrip := func(id string) {
		t.Helper()
		initiator, err := tunnel.NewInitiator(key, machineID)
		if err != nil {
			t.Fatal(err)
		}
		var channel *tunnel.Channel
		timeout := time.After(5 * time.Second)
		resend := time.NewTicker(100 * time.Millisecond)
		defer resend.Stop()
		write(initiator.Hello())
		for channel == nil {
			select {
			case frame := <-frames:
				if tunnel.FrameKind(frame) == tunnel.KindAck {
					// Acks for the replayed hello may still be queued; skip them.
					channel, _ = initiator.Finish(frame)
				}
			case <-resend.C:
				write(initiator.Hello())
			case <-timeout:
				t.Fatalf("%s: no ack from daemon", id)
			}
		}
		request, err := channel.Seal(protocol.Message{Type: protocol.TypeRequest, ID: id, Method: protocol.MethodListSessions})
		if err != nil {
			t.Fatal(err)
		}
		write(request)
		for {
			select {
			case frame := <-frames:
				if tunnel.FrameKind(frame) != tunnel.KindData {
					continue
				}
				resp, err := channel.Open(frame)
				if err != nil {
					t.Fatalf("%s: open reply: %v", id, err)
				}
				if resp.ID == id {
					return
				}
			case <-timeout:
				t.Fatalf("%s: no reply from daemon", id)
			}
		}
	}

	// 1. The bare request, re-sent while the daemon reconnects and after.
	for range 15 {
		write(recordedRequest)
		expectNoReply(100*time.Millisecond, "a replayed request after reconnecting")
	}

	// 2. The handshake that preceded it, then the request. The daemon treats
	// the hello as a retransmission and keeps the channel, sequence numbers
	// included, so the request is still stale.
	acked := false
	for i := 0; i < 50 && !acked; i++ {
		write(recordedHello)
		select {
		case frame := <-frames:
			acked = tunnel.FrameKind(frame) == tunnel.KindAck
		case <-time.After(100 * time.Millisecond):
		}
	}
	if !acked {
		t.Fatal("daemon never answered the replayed hello; it may not be attached")
	}
	write(recordedRequest)
	expectNoReply(500*time.Millisecond, "a replayed request after its replayed hello")

	// 3. Once another client has attached, the recorded hello starts a fresh
	// channel whose keys the recorded request was never sealed for.
	roundTrip("after-replays")
	write(recordedHello)
	write(recordedRequest)
	expectNoReply(500*time.Millisecond, "a replayed request on a channel opened by a replayed hello")

	roundTrip("still-working")
}
