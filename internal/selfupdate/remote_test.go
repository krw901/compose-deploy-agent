package selfupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRepo mimics GitHub Releases: latest is a JSON tag, the binary
// redirects once, and the checksum is a sha256sum file.
type fakeRepo struct {
	tag      string
	missing  bool
	binary   []byte
	checksum string
}

func (repo *fakeRepo) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/krwu/compose-deploy-agent/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		if !requireAgent(w, r) {
			return
		}
		if repo.missing {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `{"tag_name":%q}`, repo.tag)
	})
	mux.HandleFunc("GET /krwu/compose-deploy-agent/releases/download/1.2.0/deploy-agent.sha256", func(w http.ResponseWriter, r *http.Request) {
		if !requireAgent(w, r) {
			return
		}
		if repo.checksum == "" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, "%s  deploy-agent\n", repo.checksum)
	})
	mux.HandleFunc("GET /krwu/compose-deploy-agent/releases/download/1.2.0/deploy-agent", func(w http.ResponseWriter, r *http.Request) {
		if !requireAgent(w, r) {
			return
		}
		http.Redirect(w, r, "/assets/deploy-agent", http.StatusFound)
	})
	mux.HandleFunc("GET /assets/deploy-agent", func(w http.ResponseWriter, r *http.Request) {
		if !requireAgent(w, r) {
			return
		}
		w.Write(repo.binary)
	})
	return mux
}

func requireAgent(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("User-Agent") != userAgent {
		http.Error(w, "missing user agent", http.StatusBadRequest)
		return false
	}
	return true
}

func newTestRemote(t *testing.T, repo *fakeRepo) *Remote {
	t.Helper()
	srv := httptest.NewServer(repo.handler())
	t.Cleanup(srv.Close)
	return &Remote{
		Owner:      defaultOwner,
		Repo:       defaultRepo,
		WebBase:    srv.URL,
		APIBase:    srv.URL,
		HTTPClient: srv.Client(),
	}
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestRemote_LatestVersion(t *testing.T) {
	tests := []struct {
		name    string
		tag     string
		missing bool
		want    string
		wantErr bool
	}{
		{
			name: "tag name",
			tag:  "1.2.0",
			want: "1.2.0",
		},
		{
			name: "trimmed whitespace",
			tag:  " 1.2.0 \r",
			want: "1.2.0",
		},
		{
			name:    "missing release",
			missing: true,
			wantErr: true,
		},
		{
			name:    "blank tag",
			tag:     "  ",
			wantErr: true,
		},
		{
			name:    "invalid tag",
			tag:     "latest",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newTestRemote(t, &fakeRepo{tag: tt.tag, missing: tt.missing})
			got, err := r.LatestVersion(t.Context())
			if tt.wantErr {
				if err == nil {
					t.Fatalf("LatestVersion() = %v, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("LatestVersion(): %v", err)
			}
			if got.String() != tt.want {
				t.Fatalf("LatestVersion() = %v, want %s", got, tt.want)
			}
		})
	}
}

func TestRemote_Download(t *testing.T) {
	binary := []byte("#!/bin/sh\necho 1.2.0\n")
	v := Version{
		Major: 1,
		Minor: 2,
	}

	tests := []struct {
		name     string
		checksum string
		wantErr  string
	}{
		{
			name:     "checksum matches",
			checksum: sha256Hex(binary),
		},
		{
			name:     "checksum file is case-insensitive",
			checksum: strings.ToUpper(sha256Hex(binary)),
		},
		{
			name:     "checksum mismatch",
			checksum: sha256Hex([]byte("other")),
			wantErr:  "checksum mismatch",
		},
		{
			name:     "no checksum file",
			checksum: "",
			wantErr:  "unexpected status",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newTestRemote(t, &fakeRepo{
				binary:   binary,
				checksum: tt.checksum,
			})
			dir := t.TempDir()

			path, err := r.Download(t.Context(), v, dir)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Download() error = %v, want containing %q", err, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Download() error = %v, want containing %q", err, tt.wantErr)
				}
				entries, _ := os.ReadDir(dir)
				if len(entries) != 0 {
					t.Fatalf("expected temp file to be cleaned up, found %v", entries)
				}
				return
			}
			if err != nil {
				t.Fatalf("Download(): %v", err)
			}
			if filepath.Dir(path) != dir {
				t.Fatalf("downloaded to %s, want a file in %s", path, dir)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(binary) {
				t.Fatalf("downloaded content = %q, want %q", got, binary)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o755 {
				t.Fatalf("mode = %v, want 0755", info.Mode().Perm())
			}
		})
	}
}
