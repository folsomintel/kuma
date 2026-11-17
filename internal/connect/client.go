package connect

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/folsomintel/kuma/internal/protocol"
	"github.com/folsomintel/kuma/internal/tunnel"
	"github.com/folsomintel/kuma/internal/wsutil"
)

// helloRetry is how often the hello is resent while waiting for the daemon.
// The relay drops frames when the daemon is not attached, so one send is not
// enough.
const helloRetry = 250 * time.Millisecond

// Client is an encrypted protocol client attached to the relay as role=client.
type Client struct {
	cred      *Credentials
	initiator *tunnel.Initiator

	// connMu guards conn and channel, and serializes every write so frames
	// reach the wire in the order their sequence numbers were assigned.
	connMu sync.Mutex
	conn   *websocket.Conn
	// channel is nil until the daemon acknowledges the handshake.
	channel *tunnel.Channel

	ready chan struct{} // closed once channel is set
	done  chan struct{} // closed when the read loop exits

	pendingMu sync.Mutex
	pending   map[string]chan protocol.Message

	inbox chan protocol.Message
}

// Dial connects to the relay and starts the read loop. Call WaitReady before
// anything else: no request can be sent until the tunnel is established.
func Dial(ctx context.Context, cred *Credentials) (*Client, error) {
	if cred == nil {
		return nil, fmt.Errorf("credentials are required")
	}
	if err := cred.Validate(); err != nil {
		return nil, err
	}
	initiator, err := tunnel.NewInitiator(cred.KeyBytes(), cred.MachineID)
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
		cred:      cred,
		initiator: initiator,
		conn:      conn,
		ready:     make(chan struct{}),
		done:      make(chan struct{}),
		pending:   make(map[string]chan protocol.Message),
		inbox:     make(chan protocol.Message, 64),
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

// WaitReady establishes the tunnel with the daemon, if it is not up already,
// and returns the daemon's agent list.
func (c *Client) WaitReady(ctx context.Context) (protocol.ListAgentsResult, error) {
	if err := c.handshake(ctx); err != nil {
		return protocol.ListAgentsResult{}, fmt.Errorf("daemon not ready: %w", err)
	}
	return c.ListAgents(ctx)
}

// handshake resends the hello until the daemon acknowledges it.
func (c *Client) handshake(ctx context.Context) error {
	ticker := time.NewTicker(helloRetry)
	defer ticker.Stop()
	for {
		select {
		case <-c.ready:
			return nil
		default:
		}
		if err := c.writeFrame(c.initiator.Hello()); err != nil {
			return err
		}
		select {
		case <-c.ready:
			return nil
		case <-c.done:
			return errConnectionClosed
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
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
			return protocol.Message{}, errConnectionClosed
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
	case <-c.done:
		return protocol.Message{}, errConnectionClosed
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
	c.connMu.Lock()
	defer c.connMu.Unlock()
	if c.conn == nil {
		return errNotConnected
	}
	if c.channel == nil {
		return fmt.Errorf("tunnel not established; call WaitReady first")
	}
	frame, err := c.channel.Seal(msg)
	if err != nil {
		return err
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(wsutil.WriteWait))
	return c.conn.WriteMessage(websocket.BinaryMessage, frame)
}

// writeFrame sends an already-built frame (the handshake hello).
func (c *Client) writeFrame(frame []byte) error {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	if c.conn == nil {
		return errNotConnected
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(wsutil.WriteWait))
	return c.conn.WriteMessage(websocket.BinaryMessage, frame)
}

// finishHandshake installs the channel from the daemon's ack. Later acks,
// which answer resent hellos and carry the same keys, are ignored, as is
// anything that does not answer this client's hello.
func (c *Client) finishHandshake(ack []byte) {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	if c.channel != nil {
		return
	}
	channel, err := c.initiator.Finish(ack)
	if err != nil {
		return
	}
	c.channel = channel
	close(c.ready)
}

func (c *Client) readLoop() {
	defer close(c.done)
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
		switch tunnel.FrameKind(payload) {
		case tunnel.KindAck:
			c.finishHandshake(payload)
			continue
		case tunnel.KindData:
		default:
			continue
		}
		c.connMu.Lock()
		channel := c.channel
		c.connMu.Unlock()
		if channel == nil {
			continue
		}
		msg, err := channel.Open(payload)
		if err != nil {
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

var (
	errNotConnected     = errors.New("not connected")
	errConnectionClosed = errors.New("connection closed")
)

func newRequestID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
