package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/LipJ01/fly-ssh-bastion/internal/config"
	"github.com/LipJ01/fly-ssh-bastion/internal/db"
)

func setupTestServer(t *testing.T) (*httptest.Server, *db.DB) {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	keysDir := filepath.Join(dir, "keys")
	os.MkdirAll(keysDir, 0755)

	gen := config.NewGenerator(
		filepath.Join(dir, "sshpiper.yaml"),
		keysDir,
		filepath.Join(dir, "server-key"),
	)

	router := NewRouter(database, gen, "test-secret", "test.example.com", nil)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	return server, database
}

func TestStatusEndpoint(t *testing.T) {
	srv, _ := setupTestServer(t)

	resp, err := http.Get(srv.URL + "/api/status")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", body["status"])
	}
}

func TestRegisterRequiresAuth(t *testing.T) {
	srv, _ := setupTestServer(t)

	payload := `{"name":"test","owner":"alice","local_user":"alice","public_key":"ssh-ed25519 AAAA"}`
	resp, err := http.Post(srv.URL+"/api/register", "application/json", bytes.NewBufferString(payload))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestRegisterWrongKey(t *testing.T) {
	srv, _ := setupTestServer(t)

	payload := `{"name":"test","owner":"alice","local_user":"alice","public_key":"ssh-ed25519 AAAA"}`
	req, _ := http.NewRequest("POST", srv.URL+"/api/register", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "wrong-key")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func authRequest(t *testing.T, method, url string, body any) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req, err := http.NewRequest(method, url, &buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-secret")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	return resp
}

func TestRegisterSuccess(t *testing.T) {
	srv, _ := setupTestServer(t)

	body := map[string]string{
		"name":       "alice-mac",
		"owner":      "alice",
		"local_user": "alice",
		"public_key": "ssh-ed25519 AAAA alice",
	}
	resp := authRequest(t, "POST", srv.URL+"/api/register", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		var errBody map[string]string
		json.NewDecoder(resp.Body).Decode(&errBody)
		t.Fatalf("expected 201, got %d: %v", resp.StatusCode, errBody)
	}

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)

	if result["name"] != "alice-mac" {
		t.Errorf("expected name alice-mac, got %v", result["name"])
	}
	if result["server"] != "test.example.com" {
		t.Errorf("expected server test.example.com, got %v", result["server"])
	}
	port := int(result["port"].(float64))
	if port < db.PortMin || port > db.PortMax {
		t.Errorf("port %d out of range", port)
	}
}

func TestRegisterDuplicate(t *testing.T) {
	srv, _ := setupTestServer(t)

	body := map[string]string{
		"name":       "dup",
		"owner":      "alice",
		"local_user": "alice",
		"public_key": "ssh-ed25519 AAAA",
	}

	resp := authRequest(t, "POST", srv.URL+"/api/register", body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first register failed: %d", resp.StatusCode)
	}

	resp = authRequest(t, "POST", srv.URL+"/api/register", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 for duplicate, got %d", resp.StatusCode)
	}
}

func TestRegisterMissingFields(t *testing.T) {
	srv, _ := setupTestServer(t)

	body := map[string]string{
		"name": "incomplete",
	}
	resp := authRequest(t, "POST", srv.URL+"/api/register", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestListMachines(t *testing.T) {
	srv, _ := setupTestServer(t)

	// Empty list
	resp := authRequest(t, "GET", srv.URL+"/api/machines", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var machines []map[string]any
	json.NewDecoder(resp.Body).Decode(&machines)
	if len(machines) != 0 {
		t.Fatalf("expected empty list, got %d", len(machines))
	}
}

func TestListAfterRegister(t *testing.T) {
	srv, _ := setupTestServer(t)

	// Register two machines
	for _, name := range []string{"m1", "m2"} {
		body := map[string]string{
			"name": name, "owner": "test", "local_user": "test", "public_key": "ssh-ed25519 AAAA " + name,
		}
		resp := authRequest(t, "POST", srv.URL+"/api/register", body)
		resp.Body.Close()
	}

	resp := authRequest(t, "GET", srv.URL+"/api/machines", nil)
	defer resp.Body.Close()

	var machines []map[string]any
	json.NewDecoder(resp.Body).Decode(&machines)
	if len(machines) != 2 {
		t.Fatalf("expected 2, got %d", len(machines))
	}
}

func TestDeleteMachine(t *testing.T) {
	srv, _ := setupTestServer(t)

	// Register
	body := map[string]string{
		"name": "to-delete", "owner": "test", "local_user": "test", "public_key": "ssh-ed25519 AAAA test",
	}
	resp := authRequest(t, "POST", srv.URL+"/api/register", body)
	resp.Body.Close()

	// Delete
	resp = authRequest(t, "DELETE", srv.URL+"/api/machines/to-delete", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Verify gone
	resp2 := authRequest(t, "GET", srv.URL+"/api/machines", nil)
	defer resp2.Body.Close()

	var machines []map[string]any
	json.NewDecoder(resp2.Body).Decode(&machines)
	if len(machines) != 0 {
		t.Fatalf("expected 0 after delete, got %d", len(machines))
	}
}

func TestDeleteNotFound(t *testing.T) {
	srv, _ := setupTestServer(t)

	resp := authRequest(t, "DELETE", srv.URL+"/api/machines/ghost", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestHeartbeat(t *testing.T) {
	srv, database := setupTestServer(t)

	// Register first
	regBody := map[string]string{
		"name": "hb-test", "owner": "test", "local_user": "test", "public_key": "ssh-ed25519 AAAA test",
	}
	resp := authRequest(t, "POST", srv.URL+"/api/register", regBody)
	resp.Body.Close()

	// Heartbeat
	hbBody := map[string]string{"name": "hb-test"}
	resp = authRequest(t, "POST", srv.URL+"/api/heartbeat", hbBody)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Verify last_seen updated
	m, _ := database.GetMachine("hb-test")
	if m.LastSeen == nil {
		t.Fatal("expected last_seen to be set after heartbeat")
	}
}

func TestHeartbeatNotFound(t *testing.T) {
	srv, _ := setupTestServer(t)

	body := map[string]string{"name": "ghost"}
	resp := authRequest(t, "POST", srv.URL+"/api/heartbeat", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestListRequiresAuth(t *testing.T) {
	srv, _ := setupTestServer(t)

	resp, err := http.Get(srv.URL + "/api/machines")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestStatusIsPublic(t *testing.T) {
	srv, _ := setupTestServer(t)

	// Status should work without auth
	resp, err := http.Get(srv.URL + "/api/status")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}
