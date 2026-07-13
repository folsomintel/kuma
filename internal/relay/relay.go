package relay

import (
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/folsomintel/kuma/internal/jointoken"
	"github.com/folsomintel/kuma/internal/wsutil"
)

const (
	defaultMaxRooms      = 10_000
	defaultMaxConnsPerIP = 32
	defaultHalfOpenTTL   = 5 * time.Minute
	defaultEvictInterval = 30 * time.Second
)

// Options configures the relay HTTP server.
type Options struct {
	AuthSecret            string
	AllowedOrigins        []string
	MaxRooms              int
	MaxConnsPerIP         int
	HalfOpenTTL           time.Duration
	TrustForwardedHeaders bool
	Logger                *slog.Logger
	Now                   func() time.Time
}

// Server is an opaque WebSocket message router.
type Server struct {
	log                   *slog.Logger
	authSecret            string
	allowedOrigins        map[string]struct{}
	maxRooms              int
	maxConnsPerIP         int
	halfOpenTTL           time.Duration
	trustForwardedHeaders bool
	now                   func() time.Time
	upgrader              websocket.Upgrader

	mu        sync.Mutex
	rooms     map[string]*room
	connsIP   map[string]int
	stopEvict chan struct{}
}

type room struct {
	id string
	mu sync.Mutex

	daemon *peer
	client *peer

	// halfOpenSince is set when exactly one peer is present; zero when empty or full.
	halfOpenSince time.Time
}

type peer struct {
	conn      *websocket.Conn
	send      chan []byte
	quit      chan struct{}
	closeOnce sync.Once
	ip        string
}

// NewServer creates a relay server. AuthSecret is required.
func NewServer(opts Options) *Server {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	maxRooms := opts.MaxRooms
	if maxRooms <= 0 {
		maxRooms = defaultMaxRooms
	}
	maxConns := opts.MaxConnsPerIP
	if maxConns <= 0 {
		maxConns = defaultMaxConnsPerIP
	}
	halfOpen := opts.HalfOpenTTL
	if halfOpen <= 0 {
		halfOpen = defaultHalfOpenTTL
	}

	allowed := make(map[string]struct{}, len(opts.AllowedOrigins))
	for _, origin := range opts.AllowedOrigins {
		origin = strings.TrimSpace(origin)
		if origin != "" {
			allowed[origin] = struct{}{}
		}
	}

	s := &Server{
		log:                   log,
		authSecret:            strings.TrimSpace(opts.AuthSecret),
		allowedOrigins:        allowed,
		maxRooms:              maxRooms,
		maxConnsPerIP:         maxConns,
		halfOpenTTL:           halfOpen,
		trustForwardedHeaders: opts.TrustForwardedHeaders,
		now:                   now,
		rooms:                 make(map[string]*room),
		connsIP:               make(map[string]int),
		stopEvict:             make(chan struct{}),
	}
	s.upgrader = websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin:     s.checkOrigin,
	}
	go s.evictLoop()
	return s
}

// Close stops background eviction.
func (s *Server) Close() {
	select {
	case <-s.stopEvict:
	default:
		close(s.stopEvict)
	}
}

func (s *Server) checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if len(s.allowedOrigins) == 0 {
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		return strings.EqualFold(u.Host, r.Host)
	}
	_, ok := s.allowedOrigins[origin]
	return ok
}

// Handler returns the HTTP mux for the relay.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReady)
	mux.HandleFunc("GET /ws/", s.handleWS)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleReady(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	machineID, role, ok := parseWSPath(r.URL.Path)
	if !ok {
		http.Error(w, "invalid path, expected /ws/{machineID}/{daemon|client}", http.StatusBadRequest)
		return
	}
	if s.authSecret == "" {
		http.Error(w, "relay auth not configured", http.StatusServiceUnavailable)
		return
	}
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if !jointoken.Valid(s.authSecret, machineID, role, token) {
		http.Error(w, "invalid join token", http.StatusUnauthorized)
		return
	}

	ip := clientIP(r, s.trustForwardedHeaders)
	if !s.acquireIP(ip) {
		http.Error(w, "too many connections from ip", http.StatusServiceUnavailable)
		return
	}
	releasedIP := false
	defer func() {
		if !releasedIP {
			s.releaseIP(ip)
		}
	}()

	rm, errStatus, errMsg := s.prepareAttach(machineID, role)
	if errStatus != 0 {
		http.Error(w, errMsg, errStatus)
		return
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.Warn("websocket upgrade failed", "err", err)
		s.abandonEmptyRoom(rm)
		return
	}

	p := &peer{
		conn: conn,
		send: make(chan []byte, 32),
		quit: make(chan struct{}),
		ip:   ip,
	}

	if !rm.tryAttach(role, p, s.now()) {
		_ = conn.Close()
		return
	}
	releasedIP = true // released on peer close
	s.log.Info("peer connected", "machine_id", machineID, "role", role)

	go p.writePump()
	p.readPump(s, rm, role)
}

func (s *Server) abandonEmptyRoom(rm *room) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.rooms[rm.id]
	if !ok || current != rm {
		return
	}
	rm.mu.Lock()
	empty := rm.daemon == nil && rm.client == nil
	rm.mu.Unlock()
	if empty {
		delete(s.rooms, rm.id)
	}
}

func (s *Server) prepareAttach(machineID, role string) (*room, int, string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rm, exists := s.rooms[machineID]
	if !exists {
		if len(s.rooms) >= s.maxRooms {
			return nil, http.StatusServiceUnavailable, "too many rooms"
		}
		rm = &room{id: machineID}
		s.rooms[machineID] = rm
	}

	rm.mu.Lock()
	defer rm.mu.Unlock()
	var occupied bool
	switch role {
	case jointoken.RoleDaemon:
		occupied = rm.daemon != nil
	case jointoken.RoleClient:
		occupied = rm.client != nil
	}
	if occupied {
		return nil, http.StatusConflict, "role already connected"
	}
	return rm, 0, ""
}

func (rm *room) tryAttach(role string, p *peer, now time.Time) bool {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	switch role {
	case jointoken.RoleDaemon:
		if rm.daemon != nil {
			return false
		}
		rm.daemon = p
	case jointoken.RoleClient:
		if rm.client != nil {
			return false
		}
		rm.client = p
	default:
		return false
	}
	rm.updateHalfOpenLocked(now)
	return true
}

func (rm *room) updateHalfOpenLocked(now time.Time) {
	one := (rm.daemon != nil) != (rm.client != nil)
	if one {
		if rm.halfOpenSince.IsZero() {
			rm.halfOpenSince = now
		}
		return
	}
	rm.halfOpenSince = time.Time{}
}

func parseWSPath(path string) (machineID, role string, ok bool) {
	path = strings.TrimPrefix(path, "/ws/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 {
		return "", "", false
	}
	machineID = parts[0]
	role = parts[1]
	if machineID == "" || (role != jointoken.RoleDaemon && role != jointoken.RoleClient) {
		return "", "", false
	}
	return machineID, role, true
}

func (s *Server) maybeDeleteRoom(rm *room) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.rooms[rm.id]
	if !ok || current != rm {
		return
	}
	rm.mu.Lock()
	empty := rm.daemon == nil && rm.client == nil
	rm.mu.Unlock()
	if empty {
		delete(s.rooms, rm.id)
		s.log.Info("room destroyed", "machine_id", rm.id)
	}
}

func (rm *room) detach(role string, p *peer, now time.Time) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	switch role {
	case jointoken.RoleDaemon:
		if rm.daemon == p {
			rm.daemon = nil
		}
	case jointoken.RoleClient:
		if rm.client == p {
			rm.client = nil
		}
	}
	rm.updateHalfOpenLocked(now)
}

func (rm *room) peer(role string) *peer {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if role == jointoken.RoleDaemon {
		return rm.daemon
	}
	return rm.client
}

func (p *peer) readPump(s *Server, rm *room, role string) {
	defer func() {
		rm.detach(role, p, s.now())
		p.close()
		s.releaseIP(p.ip)
		s.maybeDeleteRoom(rm)
		s.log.Info("peer disconnected", "machine_id", rm.id, "role", role)
	}()

	p.conn.SetReadLimit(wsutil.MaxMessageSize)
	_ = p.conn.SetReadDeadline(time.Now().Add(wsutil.PongWait))
	p.conn.SetPongHandler(func(string) error {
		return p.conn.SetReadDeadline(time.Now().Add(wsutil.PongWait))
	})

	for {
		msgType, payload, err := p.conn.ReadMessage()
		if err != nil {
			return
		}
		if msgType != websocket.BinaryMessage {
			s.log.Warn("dropping non-binary frame", "machine_id", rm.id, "role", role, "type", msgType)
			continue
		}

		targetRole := jointoken.RoleClient
		if role == jointoken.RoleClient {
			targetRole = jointoken.RoleDaemon
		}
		target := rm.peer(targetRole)
		if target == nil {
			continue
		}
		if !target.enqueue(payload) {
			s.log.Warn("disconnecting slow peer; send buffer full", "machine_id", rm.id, "role", targetRole)
			target.close()
		}
	}
}

func (p *peer) enqueue(msg []byte) bool {
	select {
	case <-p.quit:
		return false
	case p.send <- msg:
		return true
	default:
		return false
	}
}

func (p *peer) writePump() {
	ticker := time.NewTicker(wsutil.PingPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-p.quit:
			return
		case msg := <-p.send:
			_ = p.conn.SetWriteDeadline(time.Now().Add(wsutil.WriteWait))
			if err := p.conn.WriteMessage(websocket.BinaryMessage, msg); err != nil {
				p.close()
				return
			}
		case <-ticker.C:
			_ = p.conn.SetWriteDeadline(time.Now().Add(wsutil.WriteWait))
			if err := p.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				p.close()
				return
			}
		}
	}
}

func (p *peer) close() {
	p.closeOnce.Do(func() {
		close(p.quit)
		_ = p.conn.Close()
	})
}

func (s *Server) acquireIP(ip string) bool {
	if ip == "" {
		ip = "unknown"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.connsIP[ip] >= s.maxConnsPerIP {
		return false
	}
	s.connsIP[ip]++
	return true
}

func (s *Server) releaseIP(ip string) {
	if ip == "" {
		ip = "unknown"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.connsIP[ip]
	if n <= 1 {
		delete(s.connsIP, ip)
		return
	}
	s.connsIP[ip] = n - 1
}

func clientIP(r *http.Request, trustForwarded bool) string {
	if trustForwarded {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[0])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) evictLoop() {
	ticker := time.NewTicker(defaultEvictInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopEvict:
			return
		case <-ticker.C:
			s.evictHalfOpen()
		}
	}
}

func (s *Server) evictHalfOpen() {
	now := s.now()
	type doomed struct {
		rm   *room
		role string
		p    *peer
	}
	var toClose []doomed

	s.mu.Lock()
	for _, rm := range s.rooms {
		rm.mu.Lock()
		if !rm.halfOpenSince.IsZero() && now.Sub(rm.halfOpenSince) > s.halfOpenTTL {
			if rm.daemon != nil && rm.client == nil {
				toClose = append(toClose, doomed{rm: rm, role: jointoken.RoleDaemon, p: rm.daemon})
			} else if rm.client != nil && rm.daemon == nil {
				toClose = append(toClose, doomed{rm: rm, role: jointoken.RoleClient, p: rm.client})
			}
		}
		rm.mu.Unlock()
	}
	s.mu.Unlock()

	for _, d := range toClose {
		s.log.Info("evicting half-open peer", "machine_id", d.rm.id, "role", d.role)
		d.p.close()
	}
}
