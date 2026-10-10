package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/krwu/compose-deploy-agent/internal/api"
)

var shortTimeouts = serverTimeouts{
	readHeader: 200 * time.Millisecond,
	read:       time.Second,
	write:      time.Second,
	idle:       time.Second,
}

// assertServerClosesStalledConn writes prefix, then stalls, and expects
// the server to drop the connection well before the client gives up.
func assertServerClosesStalledConn(t *testing.T, addr string, prefix []byte) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_, err = conn.Write(prefix)
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	start := time.Now()
	err = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	_, err = io.ReadAll(conn)
	if errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("server kept a stalled connection open for %s", time.Since(start))
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("server took %s to drop a stalled connection", elapsed)
	}
}

func TestServer_SlowHeadersTimeOut(t *testing.T) {
	h := startHarnessWith(t, harnessOptions{timeouts: &shortTimeouts})
	// Headers are never terminated by the blank line (slowloris).
	assertServerClosesStalledConn(t, h.addr, []byte("GET /v1/version HTTP/1.1\r\nHost: agent\r\n"))
}

func TestServer_StalledTLSHandshakeTimesOut(t *testing.T) {
	h := startHarnessWith(t, harnessOptions{timeouts: &shortTimeouts})
	// A TLS record header byte routes the connection to the TLS side,
	// where the handshake then never progresses.
	assertServerClosesStalledConn(t, h.addr, []byte{tlsRecordTypeHandshake})
}

func TestServer_OversizedBodyRejected(t *testing.T) {
	h := startHarness(t)
	client := h.httpClient(h.issueClient(t, "ci-lss-web"))

	body := `{"service":"svc","tag":"` + strings.Repeat("a", maxRequestBodyBytes) + `"}`
	resp, err := client.Post("https://"+h.addr+"/v1/deploy", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /v1/deploy: %v", err)
	}
	defer resp.Body.Close()
	assertErrorCode(t, resp, http.StatusRequestEntityTooLarge, api.ErrBodyTooLarge)
}

// TestServer_RequestCannotInjectOptions ensures a deploy request can carry
// nothing but service and tag/digest: extra fields such as a post_deploy
// user or command are rejected instead of silently ignored.
func TestServer_RequestCannotInjectOptions(t *testing.T) {
	h := startHarness(t)
	client := h.httpClient(h.issueClient(t, "ci-lss-web"))

	bodies := []string{
		`{"service":"svc","tag":"abc1234","user":"root"}`,
		`{"service":"svc","tag":"abc1234","args":["sh","-c","id"]}`,
		`{"service":"svc","tag":"abc1234","post_deploy":{"user":"root","args":["sh"]}}`,
		`{"service":"svc","tag":"abc1234","digest":"sha256:` + strings.Repeat("a", 64) + `"}`,
		`{"service":"svc"}`,
	}
	for _, body := range bodies {
		resp, err := client.Post("https://"+h.addr+"/v1/deploy", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("POST /v1/deploy: %v", err)
		}
		assertErrorCode(t, resp, http.StatusBadRequest, api.ErrInvalidParam)
		resp.Body.Close()
	}
}

func TestServer_DigestRejectedForTagService(t *testing.T) {
	h := startHarness(t)
	client := h.httpClient(h.issueClient(t, "ci-lss-web"))

	body, _ := json.Marshal(api.DeployRequest{
		Service: "svc",
		Digest:  "sha256:" + strings.Repeat("a", 64),
	})
	resp, err := client.Post("https://"+h.addr+"/v1/deploy", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /v1/deploy: %v", err)
	}
	defer resp.Body.Close()
	assertErrorCode(t, resp, http.StatusBadRequest, api.ErrInvalidParam)
}

func assertErrorCode(t *testing.T, resp *http.Response, wantStatus int, wantCode api.ErrorCode) {
	t.Helper()
	if resp.StatusCode != wantStatus {
		t.Fatalf("expected status %d, got %d", wantStatus, resp.StatusCode)
	}
	var errResp api.ErrorResponse
	err := json.NewDecoder(resp.Body).Decode(&errResp)
	if err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if errResp.Code != wantCode {
		t.Fatalf("expected code %s, got %s (%s)", wantCode, errResp.Code, errResp.Message)
	}
}

func TestServer_TrailingDataRejected(t *testing.T) {
	h := startHarness(t)
	client := h.httpClient(h.issueClient(t, "ci-lss-web"))

	valid := `{"service":"svc","tag":"abc1234"}`
	for _, body := range []string{
		valid + valid,
		valid + ` garbage`,
		valid + `]`,
		valid + `{"service":"svc","tag":"zzz9999"}`,
	} {
		resp, err := client.Post("https://"+h.addr+"/v1/deploy", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("POST /v1/deploy: %v", err)
		}
		assertErrorCode(t, resp, http.StatusBadRequest, api.ErrInvalidParam)
		resp.Body.Close()
	}

	resp, err := client.Post("https://"+h.addr+"/v1/deploy", "application/json", strings.NewReader(valid+" \n\t\r\n"))
	if err != nil {
		t.Fatalf("POST /v1/deploy: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("trailing whitespace must be accepted, got %d", resp.StatusCode)
	}
}

// TestServer_ConnectionCap fills every connection slot with idle
// connections and checks that a further client is only served once a slot
// is freed.
func TestServer_ConnectionCap(t *testing.T) {
	h := startHarnessWith(t, harnessOptions{maxConns: 2})

	held := make([]net.Conn, 0, 2)
	for range 2 {
		conn, err := net.Dial("tcp", h.addr)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		t.Cleanup(func() { conn.Close() })
		held = append(held, conn)
	}

	conn, err := net.Dial("tcp", h.addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_, err = conn.Write([]byte("GET /v1/version HTTP/1.1\r\nHost: agent\r\nConnection: close\r\n\r\n"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 64)
	err = conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	_, err = conn.Read(buf)
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("expected the third connection to wait for a slot, got err=%v", err)
	}

	held[0].Close()
	err = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("expected a response once a slot was freed: %v", err)
	}
	if !strings.HasPrefix(string(buf[:n]), "HTTP/1.1 200") {
		t.Fatalf("unexpected response %q", buf[:n])
	}
}
