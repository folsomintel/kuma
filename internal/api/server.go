package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"kuma/internal/crypto"
	"kuma/internal/fuse"
	"kuma/internal/jointoken"
	"kuma/internal/store"
)

// Server is the kuma control-plane HTTP API.
type Server struct {
	cfg   *Config
	log   *slog.Logger
	store store.Store
	fuse  fuse.Client
}

// NewServer constructs the API server.
func NewServer(cfg *Config, st store.Store, fuseClient fuse.Client, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{cfg: cfg, log: log, store: st, fuse: fuseClient}
}

// Handler returns the authenticated HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReady)

	mux.HandleFunc("GET /v1/devices", s.handleListDevices)
	mux.HandleFunc("POST /v1/devices", s.handleCreateDevice)
	mux.HandleFunc("GET /v1/devices/{id}", s.handleGetDevice)
	mux.HandleFunc("DELETE /v1/devices/{id}", s.handleDeleteDevice)

	mux.HandleFunc("GET /v1/cloud-agents", s.handleListCloudAgents)
	mux.HandleFunc("POST /v1/cloud-agents", s.handleCreateCloudAgent)
	mux.HandleFunc("GET /v1/cloud-agents/{id}", s.handleGetCloudAgent)
	mux.HandleFunc("DELETE /v1/cloud-agents/{id}", s.handleDeleteCloudAgent)

	return s.withAuth(mux)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeText(w, http.StatusOK, "ok")
}

func (s *Server) handleReady(w http.ResponseWriter, _ *http.Request) {
	writeText(w, http.StatusOK, "ok")
}

type createDeviceRequest struct {
	Name string `json:"name"`
}

type deviceResponse struct {
	ID              string     `json:"id"`
	Name            string     `json:"name,omitempty"`
	MachineID       string     `json:"machine_id"`
	Kind            store.Kind `json:"kind"`
	Key             string     `json:"key,omitempty"`
	DaemonJoinToken string     `json:"daemon_join_token,omitempty"`
	ClientJoinToken string     `json:"client_join_token,omitempty"`
	RelayURL        string     `json:"relay_url"`
	CreatedAt       time.Time  `json:"created_at"`
}

func (s *Server) handleCreateDevice(w http.ResponseWriter, r *http.Request) {
	var req createDeviceRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	machineID, err := randomID("m")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to allocate machine id")
		return
	}

	keyBytes, err := crypto.GenerateKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate key")
		return
	}
	id, err := randomID("dev")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to allocate id")
		return
	}

	daemonTok, clientTok, err := s.joinTokens(machineID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to mint join tokens")
		return
	}

	d := store.Device{
		ID:        id,
		Name:      strings.TrimSpace(req.Name),
		MachineID: machineID,
		Key:       crypto.EncodeKey(keyBytes),
		Kind:      store.KindDevice,
		CreatedAt: Now().UTC(),
	}
	if err := s.store.CreateDevice(d); err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			writeError(w, http.StatusConflict, "device already exists")
			return
		}
		s.log.Error("create device failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSONNoStore(w, http.StatusCreated, deviceResponse{
		ID:              d.ID,
		Name:            d.Name,
		MachineID:       d.MachineID,
		Kind:            d.Kind,
		Key:             d.Key,
		DaemonJoinToken: daemonTok,
		ClientJoinToken: clientTok,
		RelayURL:        s.cfg.RelayURL,
		CreatedAt:       d.CreatedAt,
	})
}

func (s *Server) handleListDevices(w http.ResponseWriter, _ *http.Request) {
	list := s.store.ListDevices()
	out := make([]deviceResponse, 0, len(list))
	for _, d := range list {
		out = append(out, deviceResponse{
			ID:        d.ID,
			Name:      d.Name,
			MachineID: d.MachineID,
			Kind:      d.Kind,
			RelayURL:  s.cfg.RelayURL,
			CreatedAt: d.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": out})
}

func (s *Server) handleGetDevice(w http.ResponseWriter, r *http.Request) {
	d, err := s.store.GetDevice(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "device not found")
		return
	}
	writeJSON(w, http.StatusOK, deviceResponse{
		ID:        d.ID,
		Name:      d.Name,
		MachineID: d.MachineID,
		Kind:      d.Kind,
		RelayURL:  s.cfg.RelayURL,
		CreatedAt: d.CreatedAt,
	})
}

func (s *Server) handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteDevice(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, "device not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type createCloudAgentRequest struct {
	Name      string `json:"name"`
	CPUs      int32  `json:"cpus"`
	RamMB     int32  `json:"ram_mb"`
	StorageGB int32  `json:"storage_gb"`
}

type cloudAgentResponse struct {
	ID                string    `json:"id"`
	Name              string    `json:"name,omitempty"`
	DeviceID          string    `json:"device_id"`
	MachineID         string    `json:"machine_id"`
	Key               string    `json:"key,omitempty"`
	DaemonJoinToken   string    `json:"daemon_join_token,omitempty"`
	ClientJoinToken   string    `json:"client_join_token,omitempty"`
	RelayURL          string    `json:"relay_url"`
	FuseEnvironmentID string    `json:"fuse_environment_id"`
	FuseState         string    `json:"fuse_state"`
	FuseURL           string    `json:"fuse_url,omitempty"`
	FuseError         string    `json:"fuse_error,omitempty"`
	CPUs              int32     `json:"cpus"`
	RamMB             int32     `json:"ram_mb"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

func (s *Server) handleCreateCloudAgent(w http.ResponseWriter, r *http.Request) {
	var req createCloudAgentRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	cpus := req.CPUs
	if cpus <= 0 {
		cpus = s.cfg.DefaultCPUs
	}
	ram := req.RamMB
	if ram <= 0 {
		ram = s.cfg.DefaultRamMB
	}
	storage := req.StorageGB
	if storage < 0 {
		storage = s.cfg.DefaultStorageGB
	} else if storage == 0 {
		storage = s.cfg.DefaultStorageGB
	}
	if cpus > s.cfg.MaxCPUs || ram > s.cfg.MaxRamMB || storage > s.cfg.MaxStorageGB {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"resource limits exceeded (max cpus=%d ram_mb=%d storage_gb=%d)",
			s.cfg.MaxCPUs, s.cfg.MaxRamMB, s.cfg.MaxStorageGB,
		))
		return
	}

	machineID, err := randomID("cloud")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to allocate machine id")
		return
	}
	keyBytes, err := crypto.GenerateKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate key")
		return
	}
	key := crypto.EncodeKey(keyBytes)

	daemonTok, clientTok, err := s.joinTokens(machineID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to mint join tokens")
		return
	}

	deviceID, err := randomID("dev")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to allocate device id")
		return
	}
	agentID, err := randomID("ca")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to allocate agent id")
		return
	}

	now := Now().UTC()
	device := store.Device{
		ID:        deviceID,
		Name:      strings.TrimSpace(req.Name),
		MachineID: machineID,
		Key:       key,
		Kind:      store.KindCloud,
		CreatedAt: now,
	}
	if err := s.store.CreateDevice(device); err != nil {
		s.log.Error("create cloud device failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	taskID := "kuma-" + agentID
	script := buildKumadStartupScript(s.cfg.KumadDownloadURL, s.cfg.KumadDownloadSHA256, machineID, key, daemonTok, s.cfg.RelayURL)
	env, err := s.fuse.CreateEnvironment(r.Context(), fuse.CreateParams{
		TaskID:         taskID,
		CPUs:           cpus,
		RamMB:          ram,
		StorageGB:      storage,
		MaxRuntimeSecs: s.cfg.MaxRuntimeSeconds,
		Secrets: map[string]string{
			"KUMA_MACHINE_ID": machineID,
			"KUMA_KEY":        key,
			"KUMA_RELAY_URL":  s.cfg.RelayURL,
			"KUMA_JOIN_TOKEN": daemonTok,
		},
		StartupScript: script,
		GatewayURL:    s.cfg.RelayURL,
	})
	if err != nil {
		_ = s.store.DeleteDevice(deviceID)
		s.log.Error("fuse create failed", "err", err)
		writeError(w, http.StatusBadGateway, "fuse create failed")
		return
	}

	agent := store.CloudAgent{
		ID:                agentID,
		Name:              strings.TrimSpace(req.Name),
		DeviceID:          deviceID,
		MachineID:         machineID,
		Key:               key,
		RelayURL:          s.cfg.RelayURL,
		FuseEnvironmentID: env.ID,
		FuseState:         env.State,
		FuseURL:           env.URL,
		FuseError:         env.Error,
		CPUs:              cpus,
		RamMB:             ram,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := s.store.CreateCloudAgent(agent); err != nil {
		_ = s.fuse.DestroyEnvironment(context.Background(), env.ID)
		_ = s.store.DeleteDevice(deviceID)
		s.log.Error("create cloud agent failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSONNoStore(w, http.StatusCreated, cloudAgentResponse{
		ID:                agent.ID,
		Name:              agent.Name,
		DeviceID:          agent.DeviceID,
		MachineID:         agent.MachineID,
		Key:               agent.Key,
		DaemonJoinToken:   daemonTok,
		ClientJoinToken:   clientTok,
		RelayURL:          agent.RelayURL,
		FuseEnvironmentID: agent.FuseEnvironmentID,
		FuseState:         agent.FuseState,
		FuseURL:           agent.FuseURL,
		FuseError:         agent.FuseError,
		CPUs:              agent.CPUs,
		RamMB:             agent.RamMB,
		CreatedAt:         agent.CreatedAt,
		UpdatedAt:         agent.UpdatedAt,
	})
}

func (s *Server) handleListCloudAgents(w http.ResponseWriter, r *http.Request) {
	list := s.store.ListCloudAgents()
	out := make([]cloudAgentResponse, 0, len(list))
	for _, a := range list {
		a = s.refreshCloudAgent(r.Context(), a)
		out = append(out, toCloudAgentResponse(a, false))
	}
	writeJSON(w, http.StatusOK, map[string]any{"cloud_agents": out})
}

func (s *Server) handleGetCloudAgent(w http.ResponseWriter, r *http.Request) {
	a, err := s.store.GetCloudAgent(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "cloud agent not found")
		return
	}
	a = s.refreshCloudAgent(r.Context(), a)
	writeJSON(w, http.StatusOK, toCloudAgentResponse(a, false))
}

func (s *Server) handleDeleteCloudAgent(w http.ResponseWriter, r *http.Request) {
	a, err := s.store.GetCloudAgent(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "cloud agent not found")
		return
	}
	if a.FuseEnvironmentID != "" {
		if err := s.fuse.DestroyEnvironment(r.Context(), a.FuseEnvironmentID); err != nil {
			s.log.Warn("fuse destroy failed", "err", err, "fuse_id", a.FuseEnvironmentID)
			writeError(w, http.StatusBadGateway, "fuse destroy failed")
			return
		}
	}
	_ = s.store.DeleteCloudAgent(a.ID)
	_ = s.store.DeleteDevice(a.DeviceID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) refreshCloudAgent(ctx context.Context, a store.CloudAgent) store.CloudAgent {
	if a.FuseEnvironmentID == "" || s.fuse == nil {
		return a
	}
	env, err := s.fuse.GetEnvironment(ctx, a.FuseEnvironmentID)
	if err != nil {
		s.log.Warn("fuse get failed", "err", err, "fuse_id", a.FuseEnvironmentID)
		return a
	}
	a.FuseState = env.State
	a.FuseURL = env.URL
	a.FuseError = env.Error
	a.UpdatedAt = Now().UTC()
	_ = s.store.UpdateCloudAgent(a)
	return a
}

func toCloudAgentResponse(a store.CloudAgent, includeKey bool) cloudAgentResponse {
	resp := cloudAgentResponse{
		ID:                a.ID,
		Name:              a.Name,
		DeviceID:          a.DeviceID,
		MachineID:         a.MachineID,
		RelayURL:          a.RelayURL,
		FuseEnvironmentID: a.FuseEnvironmentID,
		FuseState:         a.FuseState,
		FuseURL:           a.FuseURL,
		FuseError:         a.FuseError,
		CPUs:              a.CPUs,
		RamMB:             a.RamMB,
		CreatedAt:         a.CreatedAt,
		UpdatedAt:         a.UpdatedAt,
	}
	if includeKey {
		resp.Key = a.Key
	}
	return resp
}

func (s *Server) joinTokens(machineID string) (daemonTok, clientTok string, err error) {
	daemonTok, err = jointoken.Mint(s.cfg.RelayAuthSecret, machineID, jointoken.RoleDaemon)
	if err != nil {
		return "", "", err
	}
	clientTok, err = jointoken.Mint(s.cfg.RelayAuthSecret, machineID, jointoken.RoleClient)
	if err != nil {
		return "", "", err
	}
	return daemonTok, clientTok, nil
}

func buildKumadStartupScript(downloadURL, sha256hex, machineID, key, joinToken, relayURL string) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\nset -eu\n")
	b.WriteString("mkdir -p /etc/kuma\n")
	b.WriteString("cat > /etc/kuma/config.json <<'EOF'\n")
	cfg, err := json.Marshal(map[string]string{
		"machine_id": machineID,
		"key":        key,
		"relay_url":  relayURL,
		"join_token": joinToken,
	})
	if err != nil {
		// map[string]string marshal cannot fail; keep script generation total.
		cfg = []byte("{}")
	}
	b.Write(cfg)
	b.WriteString("\nEOF\n")
	b.WriteString("chmod 600 /etc/kuma/config.json\n")
	if strings.TrimSpace(downloadURL) != "" {
		fmt.Fprintf(&b, "curl -fsSL %q -o /usr/local/bin/kumad\n", downloadURL)
		fmt.Fprintf(&b, "echo %q | sha256sum -c -\n", sha256hex+"  /usr/local/bin/kumad")
		b.WriteString("chmod +x /usr/local/bin/kumad\n")
		b.WriteString("exec /usr/local/bin/kumad -config /etc/kuma/config.json\n")
	} else {
		b.WriteString("if command -v kumad >/dev/null 2>&1; then\n")
		b.WriteString("  exec kumad -config /etc/kuma/config.json\n")
		b.WriteString("fi\n")
		b.WriteString("echo 'kumad not found; set KUMAD_DOWNLOAD_URL or bake kumad into the rootfs' >&2\n")
		b.WriteString("exit 1\n")
	}
	return b.String()
}

func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(auth, prefix) {
			writeError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		token := strings.TrimSpace(strings.TrimPrefix(auth, prefix))
		sum := sha256.Sum256([]byte(token))
		want := sha256.Sum256([]byte(s.cfg.APIToken))
		if subtle.ConstantTimeCompare(sum[:], want[:]) != 1 {
			writeError(w, http.StatusUnauthorized, "invalid bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func randomID(prefix string) (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(b[:]), nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	defer func() { _ = r.Body.Close() }()
	limited := http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	dec := json.NewDecoder(limited)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return fmt.Errorf("request body too large")
		}
		return fmt.Errorf("invalid json: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeJSONNoStore(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, v)
}

func writeText(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
