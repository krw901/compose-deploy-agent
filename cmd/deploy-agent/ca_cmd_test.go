package main

import (
	"bytes"
	"os"
	"slices"
	"testing"

	"github.com/krwu/compose-deploy-agent/internal/ca"
)

func Test_splitIPList(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "single IP",
			in:   "10.20.30.40",
			want: []string{"10.20.30.40"},
		},
		{
			name: "comma-separated",
			in:   "10.20.30.40,10.20.30.41",
			want: []string{"10.20.30.40", "10.20.30.41"},
		},
		{
			name: "spaces around entries",
			in:   " 10.20.30.40 , 10.20.30.41 ",
			want: []string{"10.20.30.40", "10.20.30.41"},
		},
		{
			name: "empty entries dropped",
			in:   "10.20.30.40,,10.20.30.41,",
			want: []string{"10.20.30.40", "10.20.30.41"},
		},
		{
			name: "empty string",
			in:   "",
			want: nil,
		},
		{
			name: "only separators",
			in:   " , ",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := splitIPList(tt.in); !slices.Equal(got, tt.want) {
				t.Fatalf("splitIPList(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func Test_runCAReissueServer_ReusesCurrentNames(t *testing.T) {
	dataDir := t.TempDir()
	mgr := ca.NewManager(dataDir)
	if err := mgr.Init([]string{"10.0.0.5", "agent.example.com"}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	before, err := os.ReadFile(mgr.ServerCertPath())
	if err != nil {
		t.Fatal(err)
	}

	if err := runCAReissueServer([]string{"--data-dir", dataDir}); err != nil {
		t.Fatalf("reissue without --ip: %v", err)
	}
	after, err := os.ReadFile(mgr.ServerCertPath())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(before, after) {
		t.Fatal("expected a new server certificate")
	}
	ips, dns, err := mgr.ServerNames()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ips, []string{"10.0.0.5"}) || !slices.Equal(dns, []string{"agent.example.com"}) {
		t.Fatalf("names after renewal = %v %v", ips, dns)
	}

	if err := runCAReissueServer([]string{"--data-dir", t.TempDir()}); err == nil {
		t.Fatal("expected an error when there is no server certificate to copy names from")
	}
}
