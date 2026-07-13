package connect

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/folsomintel/kuma/internal/crypto"
	"github.com/folsomintel/kuma/internal/protocol"
	"github.com/folsomintel/kuma/internal/wsutil"
)

// Client is an encrypted protocol client attached to the relay as role=client.
type Client struct {
	cred   *Credentials
	cipher *crypto.Cipher
	aad    []byte

	connMu sync.Mutex
	conn   *websocket.Conn

	sendSeq atomic.Uint64
	recvMu  sync.Mutex
	recvSeq uint64

	pendingMu sync.Mutex
	pending   map[string]chan protocol.Message

	inbox chan protocol.Message
}

// Dial connects to the relay and starts the read loop.
func Dial(ctx context.Context, cred *Credentials) (*Client, error) {
	if cred == nil {
		return nil, fmt.Errorf("credentials are required")
	}
	if err := cred.Validate(); err != nil {
		return nil, err
	}
	ciph, err := crypto.NewCipher(cred.KeyBytes())
	if err != nil {
		return nil, err
	}

	u, err := url.Parse(cred.RelayURL)
	if err != nil {
		return nil, fmt.Errorf("relay_url: %w", err)
	}
	u.Path = "/ws/" + cred.MachineID + "/client"
	u.RawQuery = ""
	u.Fragment = ""
	q := u.Query()
	q.Set("token", cred.JoinToken)
	u.RawQuery = q.Encode()

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("dial relay: %w", err)
	}
	conn.SetReadLimit(wsutil.MaxMessageSize)
	_ = conn.SetReadDeadline(time.Now().Add(wsutil.PongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(wsutil.PongWait))
	})

	c := &Client{
		cred:    cred,
		cipher:  ciph,
		aad:     []byte(cred.MachineID),
		conn:    conn,
		pending: make(map[string]chan protocol.Message),
		inbox:   make(chan protocol.Message, 64),
	}
	go c.readLoop()
	go c.pingLoop(ctx)
	return c, nil
}

// Close tears down the WebSocket connection.
func (c *Client) Close() error {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}

// WaitReady polls list_agents until the daemon responds or ctx is done.
func (c *Client) WaitReady(ctx context.Context) (protocol.ListAgentsResult, error) {
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return protocol.ListAgentsResult{}, fmt.Errorf("daemon not ready: %w (last: %v)", err, lastErr)
			}
			return protocol.ListAgentsResult{}, err
		}
		res, err := c.ListAgents(ctx)
		if err == nil {
			return res, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return protocol.ListAgentsResult{}, fmt.Errorf("daemon not ready: %w (last: %v)", ctx.Err(), lastErr)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// ListAgents calls list_agents.
func (c *Client) ListAgents(ctx context.Context) (protocol.ListAgentsResult, error) {
	resp, err := c.rpc(ctx, protocol.MethodListAgents, nil)
	if err != nil {
		return protocol.ListAgentsResult{}, err
	}
	return protocol.DecodeParams[protocol.ListAgentsResult](resp.Result)
}

// ListSessions calls list_sessions.
func (c *Client) ListSessions(ctx context.Context) (protocol.ListSessionsResult, error) {
	resp, err := c.rpc(ctx, protocol.MethodListSessions, nil)
	if err != nil {
		return protocol.ListSessionsResult{}, err
	}
	return protocol.DecodeParams[protocol.ListSessionsResult](resp.Result)
}

// StartSession calls start_session.
func (c *Client) StartSession(ctx context.Context, agent, cwd string) (protocol.StartSessionResult, error) {
	params, err := protocol.EncodeParams(protocol.StartSessionParams{Agent: agent, CWD: cwd})
	if err != nil {
		return protocol.StartSessionResult{}, err
	}
	resp, err := c.rpc(ctx, protocol.MethodStartSession, params)
	if err != nil {
		return protocol.StartSessionResult{}, err
	}
	return protocol.DecodeParams[protocol.StartSessionResult](resp.Result)
}

// StopSession calls stop_session.
func (c *Client) StopSession(ctx context.Context, sessionID string) error {
	params, err := protocol.EncodeParams(protocol.StopSessionParams{SessionID: sessionID})
	if err != nil {
		return err
	}
	_, err = c.rpc(ctx, protocol.MethodStopSession, params)
	return err
}

// GetHistory calls get_history.
func (c *Client) GetHistory(ctx context.Context, sessionID string) (protocol.GetHistoryResult, error) {
	params, err := protocol.EncodeParams(protocol.GetHistoryParams{SessionID: sessionID})
	if err != nil {
		return protocol.GetHistoryResult{}, err
	}
	resp, err := c.rpc(ctx, protocol.MethodGetHistory, params)
	if err != nil {
		return protocol.GetHistoryResult{}, err
	}
	return protocol.DecodeParams[protocol.GetHistoryResult](resp.Result)
}

// SendInput forwards PTY bytes to the remote session.
func (c *Client) SendInput(sessionID string, data []byte) error {
	return c.send(protocol.Message{
		Type:      protocol.TypeInput,
		SessionID: sessionID,
		Data:      data,
	})
}

// SendResize notifies the remote PTY of a local terminal size change.
func (c *Client) SendResize(sessionID string, rows, cols uint16) error {
	return c.send(protocol.Message{
		Type:      protocol.TypeResize,
		SessionID: sessionID,
		Rows:      rows,
		Cols:      cols,
	})
}

// Recv returns the next stream message (output, session_exit, etc.).
func (c *Client) Recv(ctx context.Context) (protocol.Message, error) {
	select {
	case <-ctx.Done():
		return protocol.Message{}, ctx.Err()
	case msg, ok := <-c.inbox:
		if !ok {
			return protocol.Message{}, fmt.Errorf("connection closed")
		}
		return msg, nil
	}
}

func (c *Client) rpc(ctx context.Context, method protocol.Method, params []byte) (protocol.Message, error) {
	id, err := newRequestID()
	if err != nil {
		return protocol.Message{}, err
	}
	ch := make(chan protocol.Message, 1)
	c.pendingMu.Lock()
	c.pending[id] = ch
	c.pendingMu.Unlock()
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
	}()

	req := protocol.Message{
		Type:   protocol.TypeRequest,
		ID:     id,
		Method: method,
		Params: params,
	}
	if err := c.send(req); err != nil {
		return protocol.Message{}, err
	}

	select {
	case <-ctx.Done():
		return protocol.Message{}, ctx.Err()
	case msg := <-ch:
		if msg.OK == nil || !*msg.OK {
			errMsg := msg.Error
			if errMsg == "" {
				errMsg = "request failed"
			}
			return protocol.Message{}, fmt.Errorf("%s", errMsg)
		}
		return msg, nil
	}
}

func (c *Client) send(msg protocol.Message) error {
	msg.Seq = c.sendSeq.Add(1)
	plain, err := protocol.Encode(msg)
	if err != nil {
		return err
	}
	frame, err := c.cipher.Encrypt(plain, c.aad)
	if err != nil {
		return err
	}
	c.connMu.Lock()
	defer c.connMu.Unlock()
	if c.conn == nil {
		return fmt.Errorf("not connected")
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(wsutil.WriteWait))
	return c.conn.WriteMessage(websocket.BinaryMessage, frame)
}

func (c *Client) readLoop() {
	defer close(c.inbox)
	for {
		c.connMu.Lock()
		conn := c.conn
		c.connMu.Unlock()
		if conn == nil {
			return
		}
		msgType, payload, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if msgType != websocket.BinaryMessage {
			continue
		}
		plain, err := c.cipher.Decrypt(payload, c.aad)
		if err != nil {
			continue
		}
		msg, err := protocol.Decode(plain)
		if err != nil {
			continue
		}
		c.recvMu.Lock()
		next, ok := protocol.AcceptSeq(c.recvSeq, msg.Seq)
		if ok {
			c.recvSeq = next
		}
		c.recvMu.Unlock()
		if !ok {
			continue
		}

		if msg.Type == protocol.TypeResponse {
			c.pendingMu.Lock()
			ch := c.pending[msg.ID]
			c.pendingMu.Unlock()
			if ch != nil {
				select {
				case ch <- msg:
				default:
				}
			}
			continue
		}

		select {
		case c.inbox <- msg:
		default:
		}
	}
}

func (c *Client) pingLoop(ctx context.Context) {
	ticker := time.NewTicker(wsutil.PingPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.connMu.Lock()
			conn := c.conn
			if conn != nil {
				_ = conn.SetWriteDeadline(time.Now().Add(wsutil.WriteWait))
				_ = conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(wsutil.WriteWait))
			}
			c.connMu.Unlock()
		}
	}
}

func newRequestID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
