package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/krwu/compose-deploy-agent/internal/ca"
)

// newServeFixture creates a data dir with an initialized CA for ips and a
// manifest whose compose directory and file exist.
func newServeFixture(t *testing.T, ips ...string) (manifestPath, dataDir string) {
	t.Helper()
	root := t.TempDir()
	dataDir = filepath.Join(root, "data")
	if err := ca.NewManager(dataDir).Init(ips); err != nil {
		t.Fatalf("ca init: %v", err)
	}
	composeDir := filepath.Join(root, "svc")
	if err := os.MkdirAll(composeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(composeDir, "compose.yml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := "version: 1\nservices:\n  svc:\n    dir: " + composeDir +
		"\n    file: compose.yml\n    service: svc\n    image: example.com/svc\n"
	manifestPath = filepath.Join(root, "services.yaml")
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return manifestPath, dataDir
}

func Test_testServeConfig(t *testing.T) {
	manifestPath, dataDir := newServeFixture(t, "10.0.0.5")
	cfg, err := loadServeConfig(manifestPath, dataDir)
	if err != nil {
		t.Fatalf("loadServeConfig: %v", err)
	}

	tests := []struct {
		name    string
		bind    string
		wantErr bool
	}{
		{
			name: "matching IP",
			bind: "10.0.0.5:8866",
		},
		{
			name:    "IP not in certificate",
			bind:    "10.0.0.6:8866",
			wantErr: true,
		},
		{
			name:    "hostname instead of IP",
			bind:    "localhost:8866",
			wantErr: true,
		},
		{
			name:    "missing port",
			bind:    "10.0.0.5",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := testServeConfig(cfg, tt.bind)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("testServeConfig: %v", err)
			}
		})
	}
}

func Test_testServeConfig_MultipleIPs(t *testing.T) {
	manifestPath, dataDir := newServeFixture(t, "10.20.30.40", "10.20.30.41")
	cfg, err := loadServeConfig(manifestPath, dataDir)
	if err != nil {
		t.Fatalf("loadServeConfig: %v", err)
	}

	for _, bind := range []string{"10.20.30.40:9001", "10.20.30.41:9002"} {
		if _, err := testServeConfig(cfg, bind); err != nil {
			t.Fatalf("testServeConfig(%s): %v", bind, err)
		}
	}

	_, err = testServeConfig(cfg, "10.20.30.42:9001")
	if err == nil {
		t.Fatal("expected error for an IP not in the certificate")
	}
	if want := "--ip 10.20.30.40,10.20.30.41,10.20.30.42"; !strings.Contains(err.Error(), want) {
		t.Fatalf("expected reissue hint to keep existing IPs (%q), got: %v", want, err)
	}
}

func Test_loadServeConfig_Errors(t *testing.T) {
	manifestPath, dataDir := newServeFixture(t, "10.0.0.5")

	if _, err := loadServeConfig(filepath.Join(t.TempDir(), "missing.yaml"), dataDir); err == nil {
		t.Fatal("expected error for missing manifest")
	}
	if _, err := loadServeConfig(manifestPath, t.TempDir()); err == nil {
		t.Fatal("expected error for uninitialized CA")
	}
}
