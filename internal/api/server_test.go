package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/folsomintel/kuma/internal/api"
	"github.com/folsomintel/kuma/internal/fuse"
	"github.com/folsomintel/kuma/internal/jointoken"
	"github.com/folsomintel/kuma/internal/store"
)

const testRelaySecret = "api-test-relay-secret"

func TestHealthUnauthenticated(t *testing.T) {
	srv := newTestServer(t, fuse.NewMock())
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestRequiresAuth(t *testing.T) {
	srv := newTestServer(t, fuse.NewMock())
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/v1/devices")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestCreateAndListDevice(t *testing.T) {
	srv := newTestServer(t, fuse.NewMock())
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	body := `{"name":"mac-mini","machine_id":"desk-1"}`
	resp := doJSON(t, ts, http.MethodPost, "/v1/devices", body)
	if resp.StatusCode != http.StatusBadRequest {
		// unknown field machine_id should fail DisallowUnknownFields
		t.Fatalf("status=%d body=%s (expected 400 for unknown machine_id field)", resp.StatusCode, readBody(t, resp))
	}
	_ = resp.Body.Close()

	resp = doJSON(t, ts, http.MethodPost, "/v1/devices", `{"name":"mac-mini"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("expected Cache-Control: no-store")
	}
	var created map[string]any
	decodeBody(t, resp, &created)
	machineID, _ := created["machine_id"].(string)
	if machineID == "" || machineID == "desk-1" {
		t.Fatalf("machine_id=%v (must be server-generated)", created["machine_id"])
	}
	if created["key"] == nil || created["key"] == "" {
		t.Fatal("expected key on create")
	}
	daemonTok, _ := created["daemon_join_token"].(string)
	clientTok, _ := created["client_join_token"].(string)
	if daemonTok == "" || clientTok == "" {
		t.Fatal("expected join tokens")
	}
	if !jointoken.Valid(testRelaySecret, machineID, jointoken.RoleDaemon, daemonTok) {
		t.Fatal("daemon join token invalid")
	}
	if !jointoken.Valid(testRelaySecret, machineID, jointoken.RoleClient, clientTok) {
		t.Fatal("client join token invalid")
	}
	if created["relay_url"] != "ws://relay.test" {
		t.Fatalf("relay_url=%v", created["relay_url"])
	}

	list := doJSON(t, ts, http.MethodGet, "/v1/devices", "")
	defer list.Body.Close()
	var listed struct {
		Devices []map[string]any `json:"devices"`
	}
	decodeBody(t, list, &listed)
	if len(listed.Devices) != 1 {
		t.Fatalf("devices=%d", len(listed.Devices))
	}
	if listed.Devices[0]["key"] != nil {
		t.Fatal("list should not include key")
	}
	if listed.Devices[0]["daemon_join_token"] != nil {
		t.Fatal("list should not include join tokens")
	}
}

func TestCloudAgentLifecycle(t *testing.T) {
	mock := fuse.NewMock()
	srv := newTestServer(t, mock)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp := doJSON(t, ts, http.MethodPost, "/v1/cloud-agents", `{"name":"sandbox","cpus":4,"ram_mb":4096}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var created map[string]any
	decodeBody(t, resp, &created)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("missing id")
	}
	if created["key"] == "" {
		t.Fatal("expected key")
	}
	if created["daemon_join_token"] == "" {
		t.Fatal("expected daemon join token")
	}
	if created["fuse_environment_id"] == "" {
		t.Fatal("expected fuse environment id")
	}
	if created["cpus"].(float64) != 4 {
		t.Fatalf("cpus=%v", created["cpus"])
	}

	get := doJSON(t, ts, http.MethodGet, "/v1/cloud-agents/"+id, "")
	defer get.Body.Close()
	var got map[string]any
	decodeBody(t, get, &got)
	if got["fuse_state"] != "running" {
		t.Fatalf("expected refreshed running state, got %v", got["fuse_state"])
	}
	if got["key"] != nil {
		t.Fatal("get should not include key")
	}

	del := doJSON(t, ts, http.MethodDelete, "/v1/cloud-agents/"+id, "")
	defer del.Body.Close()
	if del.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status=%d", del.StatusCode)
	}
	if len(mock.Envs()) != 0 {
		t.Fatal("expected fuse env destroyed")
	}
}

func TestCloudAgentRejectsOverLimit(t *testing.T) {
	srv := newTestServer(t, fuse.NewMock())
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp := doJSON(t, ts, http.MethodPost, "/v1/cloud-agents", `{"cpus":999,"ram_mb":999999}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
}

func TestCloudAgentCreateFailsWhenFuseFails(t *testing.T) {
	mock := fuse.NewMock()
	mock.SetCreateFail(func(fuse.CreateParams) error {
		return io.EOF
	})
	srv := newTestServer(t, mock)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp := doJSON(t, ts, http.MethodPost, "/v1/cloud-agents", `{}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	list := doJSON(t, ts, http.MethodGet, "/v1/devices", "")
	defer list.Body.Close()
	var listed struct {
		Devices []any `json:"devices"`
	}
	decodeBody(t, list, &listed)
	if len(listed.Devices) != 0 {
		t.Fatal("device should be rolled back")
	}
}

func newTestServer(t *testing.T, fuseClient fuse.Client) *api.Server {
	t.Helper()
	cfg := &api.Config{
		Addr:             ":0",
		APIToken:         "test-token",
		RelayURL:         "ws://relay.test",
		RelayAuthSecret:  testRelaySecret,
		FuseBaseURL:      "http://fuse.test",
		FuseToken:        "fuse-token",
		DefaultCPUs:      2,
		DefaultRamMB:     2048,
		DefaultStorageGB: 10,
		MaxCPUs:          8,
		MaxRamMB:         16384,
		MaxStorageGB:     100,
	}
	return api.NewServer(cfg, store.NewMemory(), fuseClient, nil)
}

func doJSON(t *testing.T, ts *httptest.Server, method, path, body string) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = bytes.NewBufferString(body)
	}
	req, err := http.NewRequest(method, ts.URL+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func decodeBody(t *testing.T, resp *http.Response, dst any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatal(err)
	}
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
