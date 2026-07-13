package daemon_test

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/folsomintel/kuma/internal/crypto"
	"github.com/folsomintel/kuma/internal/daemon"
	"github.com/folsomintel/kuma/internal/jointoken"
	"github.com/folsomintel/kuma/internal/protocol"
	"github.com/folsomintel/kuma/internal/relay"
)

const e2eSecret = "e2e-relay-secret"

func TestEndToEndEncryptedSession(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	binDir := t.TempDir()
	script := filepath.Join(binDir, "claude")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec cat\n"), 0o755); err != nil {
		t.Fatalf("write fake agent: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	relaySrv := relay.NewServer(relay.Options{AuthSecret: e2eSecret})
	t.Cleanup(relaySrv.Close)
	ts := httptest.NewServer(relaySrv.Handler())
	t.Cleanup(ts.Close)

	machineID := "e2e-machine"
	daemonTok, err := jointoken.Mint(e2eSecret, machineID, jointoken.RoleDaemon)
	if err != nil {
		t.Fatal(err)
	}
	clientTok, err := jointoken.Mint(e2eSecret, machineID, jointoken.RoleClient)
	if err != nil {
		t.Fatal(err)
	}

	wsBase := "ws" + strings.TrimPrefix(ts.URL, "http")
	cfg := &daemon.Config{
		MachineID:    machineID,
		Key:          crypto.EncodeKey(key),
		RelayURL:     wsBase,
		JoinToken:    daemonTok,
		HistoryLimit: 64 * 1024,
		MaxSessions:  8,
		MinBackoff:   50 * time.Millisecond,
		MaxBackoff:   200 * time.Millisecond,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d, err := daemon.New(cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(ctx)
	}()

	var (
		client *websocket.Conn
		sess   *clientSession
	)
	t.Cleanup(func() {
		if client != nil {
			_ = client.Close()
		}
	})

	var listResp protocol.Message
	deadline := time.Now().Add(15 * time.Second)
	for {
		select {
		case err := <-errCh:
			t.Fatalf("daemon exited early: %v", err)
		default:
		}

		if client == nil {
			var dialErr error
			client, _, dialErr = websocket.DefaultDialer.Dial(wsBase+"/ws/"+machineID+"/client?token="+clientTok, nil)
			if dialErr != nil {
				if time.Now().After(deadline) {
					t.Fatalf("dial client: %v", dialErr)
				}
				time.Sleep(50 * time.Millisecond)
				continue
			}
			sess = &clientSession{conn: client, key: key, aad: []byte(machineID)}
		}

		var err error
		listResp, err = sess.rpc(t, protocol.Message{
			Type:   protocol.TypeRequest,
			ID:     "1",
			Method: protocol.MethodListAgents,
		})
		if err == nil && listResp.OK != nil && *listResp.OK {
			break
		}
		// Drop a timed-out client socket; early frames are discarded while the
		// daemon peer is missing, and a read deadline can poison the conn.
		_ = client.Close()
		client = nil
		sess = nil
		if time.Now().After(deadline) {
			t.Fatalf("daemon never became ready: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	startParams, err := protocol.EncodeParams(protocol.StartSessionParams{Agent: "claude"})
	if err != nil {
		t.Fatalf("EncodeParams: %v", err)
	}
	startResp, err := sess.rpc(t, protocol.Message{
		Type:   protocol.TypeRequest,
		ID:     "2",
		Method: protocol.MethodStartSession,
		Params: startParams,
	})
	if err != nil {
		t.Fatalf("start_session: %v", err)
	}
	if startResp.OK == nil || !*startResp.OK {
		t.Fatalf("start_session failed: %s", startResp.Error)
	}
	startResult, err := protocol.DecodeParams[protocol.StartSessionResult](startResp.Result)
	if err != nil {
		t.Fatalf("decode start result: %v", err)
	}
	if startResult.SessionID == "" {
		t.Fatal("missing session id")
	}

	if err := sess.send(t, protocol.Message{
		Type:      protocol.TypeInput,
		SessionID: startResult.SessionID,
		Data:      []byte("hello-e2e\n"),
	}); err != nil {
		t.Fatalf("input: %v", err)
	}

	output := sess.readUntilOutput(t, startResult.SessionID, 3*time.Second)
	if !strings.Contains(string(output), "hello-e2e") {
		t.Fatalf("unexpected output %q", output)
	}

	stopParams, err := protocol.EncodeParams(protocol.StopSessionParams{SessionID: startResult.SessionID})
	if err != nil {
		t.Fatalf("EncodeParams: %v", err)
	}
	stopResp, err := sess.rpc(t, protocol.Message{
		Type:   protocol.TypeRequest,
		ID:     "3",
		Method: protocol.MethodStopSession,
		Params: stopParams,
	})
	if err != nil {
		t.Fatalf("stop_session: %v", err)
	}
	if stopResp.OK == nil || !*stopResp.OK {
		t.Fatalf("stop_session failed: %s", stopResp.Error)
	}

	exitMsg := sess.readUntilType(t, protocol.TypeSessionExit, 3*time.Second)
	if exitMsg.SessionID != startResult.SessionID {
		t.Fatalf("session_exit id=%q", exitMsg.SessionID)
	}

	cancel()
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("daemon did not stop")
	}
}

type clientSession struct {
	conn    *websocket.Conn
	key     []byte
	aad     []byte
	sendSeq atomic.Uint64
	recvSeq uint64
}

func (s *clientSession) rpc(t *testing.T, req protocol.Message) (protocol.Message, error) {
	t.Helper()
	if err := s.send(t, req); err != nil {
		return protocol.Message{}, err
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		msg, err := s.recv(t, time.Until(deadline))
		if err != nil {
			return protocol.Message{}, err
		}
		if msg.Type == protocol.TypeResponse && msg.ID == req.ID {
			return msg, nil
		}
	}
	return protocol.Message{}, context.DeadlineExceeded
}

func (s *clientSession) send(t *testing.T, msg protocol.Message) error {
	t.Helper()
	msg.Seq = s.sendSeq.Add(1)
	plain, err := protocol.Encode(msg)
	if err != nil {
		return err
	}
	frame, err := crypto.Encrypt(s.key, plain, s.aad)
	if err != nil {
		return err
	}
	_ = s.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	return s.conn.WriteMessage(websocket.BinaryMessage, frame)
}

func (s *clientSession) recv(t *testing.T, timeout time.Duration) (protocol.Message, error) {
	t.Helper()
	_ = s.conn.SetReadDeadline(time.Now().Add(timeout))
	msgType, frame, err := s.conn.ReadMessage()
	if err != nil {
		return protocol.Message{}, err
	}
	if msgType != websocket.BinaryMessage {
		t.Fatalf("expected binary frame, got %d", msgType)
	}
	if strings.Contains(string(frame), `"type"`) {
		t.Fatalf("relay frame looked like plaintext JSON")
	}
	plain, err := crypto.Decrypt(s.key, frame, s.aad)
	if err != nil {
		return protocol.Message{}, err
	}
	msg, err := protocol.Decode(plain)
	if err != nil {
		return protocol.Message{}, err
	}
	next, ok := protocol.AcceptSeq(s.recvSeq, msg.Seq)
	if !ok {
		t.Fatalf("reject seq %d (last %d)", msg.Seq, s.recvSeq)
	}
	s.recvSeq = next
	return msg, nil
}

func (s *clientSession) readUntilOutput(t *testing.T, sessionID string, timeout time.Duration) []byte {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var out []byte
	for time.Now().Before(deadline) {
		msg, err := s.recv(t, time.Until(deadline))
		if err != nil {
			t.Fatalf("recv output: %v", err)
		}
		if msg.Type == protocol.TypeOutput && msg.SessionID == sessionID {
			out = append(out, msg.Data...)
			if strings.Contains(string(out), "hello-e2e") {
				return out
			}
		}
	}
	t.Fatalf("timed out waiting for output, got %q", out)
	return nil
}

func (s *clientSession) readUntilType(t *testing.T, typ protocol.Type, timeout time.Duration) protocol.Message {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		msg, err := s.recv(t, time.Until(deadline))
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		if msg.Type == typ {
			return msg
		}
	}
	t.Fatalf("timed out waiting for %s", typ)
	return protocol.Message{}
}
