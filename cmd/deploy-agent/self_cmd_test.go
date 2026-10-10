package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/krwu/compose-deploy-agent/internal/selfupdate"
)

func setVersion(t *testing.T, v string) {
	t.Helper()
	old := version
	version = v
	t.Cleanup(func() { version = old })
}

func Test_upgradeNeeded(t *testing.T) {
	latest := selfupdate.Version{
		Major: 1,
		Minor: 2,
	}
	tests := []struct {
		name    string
		current string
		force   bool
		want    bool
		wantErr bool
	}{
		{
			name:    "older",
			current: "1.1.9",
			want:    true,
		},
		{
			name:    "equal",
			current: "1.2.0",
			want:    false,
		},
		{
			name:    "equal forced reinstall",
			current: "1.2.0",
			force:   true,
			want:    true,
		},
		{
			name:    "newer refuses downgrade",
			current: "1.10.0",
			wantErr: true,
		},
		{
			name:    "newer forced downgrade",
			current: "1.10.0",
			force:   true,
			want:    true,
		},
		{
			name:    "dev refused",
			current: "dev",
			wantErr: true,
		},
		{
			name:    "dev forced",
			current: "dev",
			force:   true,
			want:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setVersion(t, tt.current)
			got, err := upgradeNeeded(latest, tt.force)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("upgradeNeeded() = %v, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("upgradeNeeded(): %v", err)
			}
			if got != tt.want {
				t.Fatalf("upgradeNeeded() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_readBindAddr(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
		wantErr bool
	}{
		{
			name:    "plain",
			content: "# comment\nDEPLOY_AGENT_BIND=10.0.0.5:8866\n",
			want:    "10.0.0.5:8866",
		},
		{
			name:    "quoted",
			content: "DEPLOY_AGENT_BIND=\"10.0.0.5:8866\"\n",
			want:    "10.0.0.5:8866",
		},
		{
			name:    "missing",
			content: "OTHER=1\n",
			wantErr: true,
		},
		{
			name:    "empty",
			content: "DEPLOY_AGENT_BIND=\n",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "serve.env")
			if err := os.WriteFile(path, []byte(tt.content), 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := readBindAddr(path)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("readBindAddr() = %q, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("readBindAddr(): %v", err)
			}
			if got != tt.want {
				t.Fatalf("readBindAddr() = %q, want %q", got, tt.want)
			}
		})
	}
}
