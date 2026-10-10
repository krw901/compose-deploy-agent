package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/krwu/compose-deploy-agent/internal/api"
	"github.com/krwu/compose-deploy-agent/internal/config"
	"github.com/krwu/compose-deploy-agent/internal/deployer"
	"github.com/krwu/compose-deploy-agent/internal/dockerx"
	"github.com/krwu/compose-deploy-agent/internal/store"
)

func boundManifest(t *testing.T) *config.Manifest {
	t.Helper()
	svc := func() config.Service {
		return config.Service{
			Dir:            t.TempDir(),
			File:           "compose.yml",
			ComposeService: "app",
			Image:          "example.com/app",
			TagEnv:         "IMAGE_TAG",
		}
	}
	return &config.Manifest{
		Version: 1,
		Services: map[string]config.Service{
			"main": svc(),
			"pr-1": svc(),
		},
		Access: &config.Access{
			RequireBinding: true,
			Clients: map[string]config.ClientBinding{
				"ci-main":    {Services: []string{"main"}},
				"ci-preview": {Services: []string{"pr-1"}},
			},
		},
	}
}

func postDeploy(t *testing.T, client *http.Client, addr, service string) *http.Response {
	t.Helper()
	body, _ := json.Marshal(api.DeployRequest{
		Service: service,
		Tag:     "abc1234",
	})
	resp, err := client.Post("https://"+addr+"/v1/deploy", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /v1/deploy: %v", err)
	}
	return resp
}

func TestServer_CertBindingRestrictsDeploy(t *testing.T) {
	h := startHarnessWith(t, harnessOptions{manifest: boundManifest(t)})
	mainClient := h.httpClient(h.issueClient(t, "ci-main"))
	previewClient := h.httpClient(h.issueClient(t, "ci-preview"))
	legacyClient := h.httpClient(h.issueClient(t, "legacy"))

	resp := postDeploy(t, mainClient, h.addr, "main")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("bound cert deploying its own service: expected 202, got %d", resp.StatusCode)
	}
	var accepted api.DeployAccepted
	err := json.NewDecoder(resp.Body).Decode(&accepted)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	resp = postDeploy(t, previewClient, h.addr, "main")
	assertErrorCode(t, resp, http.StatusForbidden, api.ErrCertNotAllowed)
	resp.Body.Close()

	resp = postDeploy(t, legacyClient, h.addr, "pr-1")
	assertErrorCode(t, resp, http.StatusForbidden, api.ErrCertNotAllowed)
	resp.Body.Close()

	resp, err = previewClient.Get("https://" + h.addr + "/v1/status/main")
	if err != nil {
		t.Fatalf("GET status: %v", err)
	}
	assertErrorCode(t, resp, http.StatusForbidden, api.ErrCertNotAllowed)
	resp.Body.Close()

	resp, err = previewClient.Get("https://" + h.addr + "/v1/deploys/" + accepted.DeployID)
	if err != nil {
		t.Fatalf("GET deploy: %v", err)
	}
	assertErrorCode(t, resp, http.StatusForbidden, api.ErrCertNotAllowed)
	resp.Body.Close()

	resp, err = mainClient.Get("https://" + h.addr + "/v1/deploys/" + accepted.DeployID)
	if err != nil {
		t.Fatalf("GET deploy: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("owner cert reading its deploy: expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestServer_NoBindingKeepsFullAccess(t *testing.T) {
	h := startHarness(t)
	client := h.httpClient(h.issueClient(t, "any-name"))
	resp := postDeploy(t, client, h.addr, "svc")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("without access config a registered cert keeps full access, got %d", resp.StatusCode)
	}
}

// TestServer_NoIdentityDenied guards the fail-closed default: a router
// reached without any identity middleware must not let requests through.
func TestServer_NoIdentityDenied(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	manifest := boundManifest(t)
	manifest.Access = nil
	srv := New(deployer.New(manifest, dockerx.NewClient(), st), st, "test")

	body := `{"service":"main","tag":"abc1234"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/deploy", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.mainRouter().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("deploy without identity: expected 403, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/status/main", nil)
	rec = httptest.NewRecorder()
	srv.mainRouter().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status without identity: expected 403, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/version", nil)
	rec = httptest.NewRecorder()
	srv.mainRouter().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("version needs no identity, got %d", rec.Code)
	}
}

// TestServer_LocalSocketPermissionsAndAccess checks the socket is 0600,
// nothing else is created next to it, the local identity bypasses strict
// cert bindings, and the socket is removed on shutdown.
func TestServer_LocalSocketPermissionsAndAccess(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	srv := New(deployer.New(boundManifest(t), dockerx.NewClient(), st), st, "test")

	dir := privateTempDir(t)
	socketPath := filepath.Join(dir, "agent.sock")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.LocalListener(ctx, socketPath) }()
	waitForSocket(t, socketPath)

	info, err := os.Stat(socketPath)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if info.Mode().Perm() != 0o600 || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("expected a 0600 socket, got %v", info.Mode())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected only the socket in %s, got %d entries", dir, len(entries))
	}

	resp, err := unixHTTPClient(socketPath).Post("http://deploy-agent/v1/deploy", "application/json",
		strings.NewReader(`{"service":"main","tag":"abc1234"}`))
	if err != nil {
		t.Fatalf("POST over unix socket: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("local identity must keep full access under require_binding, got %d", resp.StatusCode)
	}

	cancel()
	<-errCh
	_, err = os.Stat(socketPath)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected socket removed on shutdown, stat err=%v", err)
	}
}

func waitForSocket(t *testing.T, socketPath string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, err := os.Stat(socketPath)
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("socket file %s never appeared", socketPath)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func unixHTTPClient(socketPath string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		},
		Timeout: 5 * time.Second,
	}
}
