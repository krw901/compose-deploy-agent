package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/krwu/compose-deploy-agent/internal/api"
	"github.com/krwu/compose-deploy-agent/internal/config"
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
