package daemon

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"math/big"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"kuma/internal/agents"
	"kuma/internal/crypto"
	"kuma/internal/protocol"
	"kuma/internal/pty"
	"kuma/internal/wsutil"
)

// Daemon connects to the relay and manages local agent PTY sessions.
type Daemon struct {
	cfg    *Config
	log    *slog.Logger
	aad    []byte
	cipher *crypto.Cipher

	connMu sync.Mutex
	conn   *websocket.Conn

	sendSeq atomic.Uint64
	recvMu  sync.Mutex
	recvSeq uint64

	sessionsMu sync.Mutex
	sessions   map[string]*ptySession
	reserved   int // in-flight start_session reservations
}

type ptySession struct {
	id      string
	agent   string
	session *pty.Session

	historyMu sync.Mutex
	history   []byte
}

// New creates a daemon from validated config.
func New(cfg *Config, log *slog.Logger) (*Daemon, error) {
	if log == nil {
		log = slog.Default()
	}
	ciph, err := crypto.NewCipher(cfg.KeyBytes())
	if err != nil {
		return nil, fmt.Errorf("cipher: %w", err)
	}
	return &Daemon{
		cfg:      cfg,
		log:      log,
		aad:      []byte(cfg.MachineID),
		cipher:   ciph,
		sessions: make(map[string]*ptySession),
	}, nil
}

// Run connects to the relay until ctx is cancelled.
func (d *Daemon) Run(ctx context.Context) error {
	u, err := url.Parse(d.cfg.RelayURL)
	if err != nil {
		return fmt.Errorf("relay_url: %w", err)
	}
	u.Path = "/ws/" + d.cfg.MachineID + "/daemon"
	u.RawQuery = ""
	u.Fragment = ""
	q := u.Query()
	q.Set("token", d.cfg.JoinToken)
	u.RawQuery = q.Encode()

	backoff := d.cfg.MinBackoff
	for {
		if err := ctx.Err(); err != nil {
			d.closeAllSessions()
			return err
		}

		d.log.Info("connecting to relay", "url", redactedURL(u))
		conn, _, err := websocket.DefaultDialer.DialContext(ctx, u.String(), nil)
		if err != nil {
			d.log.Warn("relay dial failed", "err", err, "backoff", backoff)
			if sleepErr := sleepCtx(ctx, jitter(backoff)); sleepErr != nil {
				d.closeAllSessions()
				return sleepErr
			}
			backoff = minDuration(backoff*2, d.cfg.MaxBackoff)
			continue
		}

		backoff = d.cfg.MinBackoff
		d.log.Info("connected to relay")
		d.resetSeq()
		d.setConn(conn)
		err = d.serveConn(ctx, conn)
		d.setConn(nil)
		_ = conn.Close()
		d.log.Warn("disconnected from relay", "err", err)

		if ctx.Err() != nil {
			d.closeAllSessions()
			return ctx.Err()
		}
		if sleepErr := sleepCtx(ctx, jitter(backoff)); sleepErr != nil {
			d.closeAllSessions()
			return sleepErr
		}
		backoff = minDuration(backoff*2, d.cfg.MaxBackoff)
	}
}

func (d *Daemon) resetSeq() {
	d.sendSeq.Store(0)
	d.recvMu.Lock()
	d.recvSeq = 0
	d.recvMu.Unlock()
}

func (d *Daemon) serveConn(ctx context.Context, conn *websocket.Conn) error {
	conn.SetReadLimit(wsutil.MaxMessageSize)
	_ = conn.SetReadDeadline(time.Now().Add(wsutil.PongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(wsutil.PongWait))
	})

	pingCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go d.pingLoop(pingCtx, conn)
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	for {
		msgType, payload, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if msgType != websocket.BinaryMessage {
			d.log.Warn("ignoring non-binary relay frame", "type", msgType)
			continue
		}
		if err := d.handleFrame(payload); err != nil {
			d.log.Warn("frame handling failed", "err", err)
		}
	}
}

func (d *Daemon) pingLoop(ctx context.Context, conn *websocket.Conn) {
	ticker := time.NewTicker(wsutil.PingPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.connMu.Lock()
			active := d.conn == conn
			if active {
				_ = conn.SetWriteDeadline(time.Now().Add(wsutil.WriteWait))
				err := conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(wsutil.WriteWait))
				if err != nil {
					d.connMu.Unlock()
					_ = conn.Close()
					return
				}
			}
			d.connMu.Unlock()
		}
	}
}

func (d *Daemon) handleFrame(frame []byte) error {
	plaintext, err := d.cipher.Decrypt(frame, d.aad)
	if err != nil {
		return fmt.Errorf("decrypt: %w", err)
	}
	msg, err := protocol.Decode(plaintext)
	if err != nil {
		return err
	}

	d.recvMu.Lock()
	next, ok := protocol.AcceptSeq(d.recvSeq, msg.Seq)
	if ok {
		d.recvSeq = next
	}
	d.recvMu.Unlock()
	if !ok {
		return fmt.Errorf("reject seq %d", msg.Seq)
	}

	switch msg.Type {
	case protocol.TypeRequest:
		return d.handleRequest(msg)
	case protocol.TypeInput:
		return d.handleInput(msg)
	case protocol.TypeResize:
		return d.handleResize(msg)
	default:
		return fmt.Errorf("unsupported inbound type %q", msg.Type)
	}
}

func (d *Daemon) handleRequest(msg protocol.Message) error {
	resp := protocol.Message{
		Type: protocol.TypeResponse,
		ID:   msg.ID,
		OK:   protocol.BoolPtr(true),
	}

	switch msg.Method {
	case protocol.MethodListAgents:
		list := agents.Detect()
		infos := make([]protocol.AgentInfo, 0, len(list))
		for _, a := range list {
			infos = append(infos, protocol.AgentInfo{
				Name:      a.Name,
				Installed: a.Installed,
				Path:      a.Path,
			})
		}
		result, err := protocol.EncodeParams(protocol.ListAgentsResult{Agents: infos})
		if err != nil {
			return d.replyError(msg.ID, "encode result failed")
		}
		resp.Result = result

	case protocol.MethodListSessions:
		d.sessionsMu.Lock()
		sessions := make([]protocol.SessionInfo, 0, len(d.sessions))
		for id, s := range d.sessions {
			sessions = append(sessions, protocol.SessionInfo{
				SessionID: id,
				Agent:     s.agent,
			})
		}
		d.sessionsMu.Unlock()
		result, err := protocol.EncodeParams(protocol.ListSessionsResult{Sessions: sessions})
		if err != nil {
			return d.replyError(msg.ID, "encode result failed")
		}
		resp.Result = result

	case protocol.MethodStartSession:
		params, err := protocol.DecodeParams[protocol.StartSessionParams](msg.Params)
		if err != nil {
			return d.replyError(msg.ID, err.Error())
		}
		if !agents.Allowed(params.Agent) {
			return d.replyError(msg.ID, "agent not allowed")
		}
		if err := d.cfg.ValidateCWD(params.CWD); err != nil {
			return d.replyError(msg.ID, err.Error())
		}
		cwd := strings.TrimSpace(params.CWD)
		if cwd != "" {
			cwd = filepath.Clean(cwd)
		}
		execPath, ok := agents.Lookup(params.Agent)
		if !ok {
			return d.replyError(msg.ID, "agent not installed")
		}

		sessionID, err := newSessionID()
		if err != nil {
			return d.replyError(msg.ID, "failed to allocate session id")
		}

		// Reserve a slot before fork/exec so concurrent starts cannot
		// exceed MaxSessions even temporarily.
		d.sessionsMu.Lock()
		if len(d.sessions)+d.reserved >= d.cfg.MaxSessions {
			d.sessionsMu.Unlock()
			return d.replyError(msg.ID, "too many sessions")
		}
		d.reserved++
		d.sessionsMu.Unlock()

		ptySess, err := pty.Start(execPath, nil, cwd)
		d.sessionsMu.Lock()
		d.reserved--
		if err != nil {
			d.sessionsMu.Unlock()
			return d.replyError(msg.ID, err.Error())
		}
		sess := &ptySession{
			id:      sessionID,
			agent:   params.Agent,
			session: ptySess,
		}
		d.sessions[sessionID] = sess
		d.sessionsMu.Unlock()
		go d.pumpSession(sess)
		result, err := protocol.EncodeParams(protocol.StartSessionResult{
			SessionID: sessionID,
			Agent:     params.Agent,
		})
		if err != nil {
			return d.replyError(msg.ID, "encode result failed")
		}
		resp.Result = result

	case protocol.MethodStopSession:
		params, err := protocol.DecodeParams[protocol.StopSessionParams](msg.Params)
		if err != nil {
			return d.replyError(msg.ID, err.Error())
		}
		if err := d.stopSession(params.SessionID); err != nil {
			return d.replyError(msg.ID, err.Error())
		}

	case protocol.MethodGetHistory:
		params, err := protocol.DecodeParams[protocol.GetHistoryParams](msg.Params)
		if err != nil {
			return d.replyError(msg.ID, err.Error())
		}
		d.sessionsMu.Lock()
		sess, ok := d.sessions[params.SessionID]
		d.sessionsMu.Unlock()
		if !ok {
			return d.replyError(msg.ID, "session not found")
		}
		sess.historyMu.Lock()
		history := append([]byte(nil), sess.history...)
		sess.historyMu.Unlock()
		result, err := protocol.EncodeParams(protocol.GetHistoryResult{History: history})
		if err != nil {
			return d.replyError(msg.ID, "encode result failed")
		}
		resp.Result = result

	default:
		return d.replyError(msg.ID, fmt.Sprintf("unknown method %q", msg.Method))
	}

	return d.send(resp)
}

func (d *Daemon) replyError(id, message string) error {
	return d.send(protocol.Message{
		Type:  protocol.TypeResponse,
		ID:    id,
		OK:    protocol.BoolPtr(false),
		Error: message,
	})
}

func (d *Daemon) handleInput(msg protocol.Message) error {
	d.sessionsMu.Lock()
	sess, ok := d.sessions[msg.SessionID]
	d.sessionsMu.Unlock()
	if !ok {
		return fmt.Errorf("unknown session %q", msg.SessionID)
	}
	_, err := sess.session.Write(msg.Data)
	return err
}

func (d *Daemon) handleResize(msg protocol.Message) error {
	d.sessionsMu.Lock()
	sess, ok := d.sessions[msg.SessionID]
	d.sessionsMu.Unlock()
	if !ok {
		return fmt.Errorf("unknown session %q", msg.SessionID)
	}
	return sess.session.Resize(msg.Rows, msg.Cols)
}

func (d *Daemon) pumpSession(sess *ptySession) {
	buf := make([]byte, 4096)
	for {
		n, err := sess.session.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			sess.historyMu.Lock()
			sess.history = append(sess.history, chunk...)
			if len(sess.history) > d.cfg.HistoryLimit {
				sess.history = sess.history[len(sess.history)-d.cfg.HistoryLimit:]
			}
			sess.historyMu.Unlock()

			_ = d.send(protocol.Message{
				Type:      protocol.TypeOutput,
				SessionID: sess.id,
				Data:      chunk,
			})
		}
		if err != nil {
			break
		}
	}

	waitErr := sess.session.Wait()
	exitCode := sess.session.ExitCode()
	_ = sess.session.Close()

	d.sessionsMu.Lock()
	delete(d.sessions, sess.id)
	d.sessionsMu.Unlock()

	d.log.Info("session exited", "session_id", sess.id, "agent", sess.agent, "exit_code", exitCode, "wait_err", waitErr)
	_ = d.send(protocol.Message{
		Type:      protocol.TypeSessionExit,
		SessionID: sess.id,
		Agent:     sess.agent,
		ExitCode:  protocol.IntPtr(exitCode),
	})
}

func (d *Daemon) stopSession(sessionID string) error {
	d.sessionsMu.Lock()
	sess, ok := d.sessions[sessionID]
	d.sessionsMu.Unlock()
	if !ok {
		return fmt.Errorf("session not found")
	}
	return sess.session.Close()
}

func (d *Daemon) closeAllSessions() {
	d.sessionsMu.Lock()
	sessions := make([]*ptySession, 0, len(d.sessions))
	for _, s := range d.sessions {
		sessions = append(sessions, s)
	}
	d.sessions = make(map[string]*ptySession)
	d.sessionsMu.Unlock()

	for _, s := range sessions {
		_ = s.session.Close()
	}
}

func (d *Daemon) send(msg protocol.Message) error {
	msg.Seq = d.sendSeq.Add(1)
	plaintext, err := protocol.Encode(msg)
	if err != nil {
		return err
	}
	frame, err := d.cipher.Encrypt(plaintext, d.aad)
	if err != nil {
		return err
	}

	d.connMu.Lock()
	defer d.connMu.Unlock()
	if d.conn == nil {
		return fmt.Errorf("not connected")
	}
	_ = d.conn.SetWriteDeadline(time.Now().Add(wsutil.WriteWait))
	return d.conn.WriteMessage(websocket.BinaryMessage, frame)
}

func (d *Daemon) setConn(conn *websocket.Conn) {
	d.connMu.Lock()
	defer d.connMu.Unlock()
	d.conn = conn
}

func newSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b[:]), nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func jitter(base time.Duration) time.Duration {
	if base <= 0 {
		return base
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(base/2)+1))
	if err != nil {
		return base
	}
	return base/2 + time.Duration(n.Int64())
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func redactedURL(u *url.URL) string {
	cp := *u
	cp.User = nil
	q := cp.Query()
	if q.Has("token") {
		q.Set("token", "redacted")
		cp.RawQuery = q.Encode()
	}
	return cp.String()
}
