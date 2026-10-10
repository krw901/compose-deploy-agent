package ca

import (
	"bytes"
	"crypto/x509"
	"net"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/krwu/compose-deploy-agent/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestInit_CreatesCAAndServerCert(t *testing.T) {
	dataDir := t.TempDir()
	mgr := NewManager(dataDir)

	if mgr.Initialized() {
		t.Fatalf("expected not initialized before Init")
	}
	if err := mgr.Init([]string{"10.0.0.5"}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if !mgr.Initialized() {
		t.Fatalf("expected initialized after Init")
	}

	if _, err := readCertPEM(mgr.CACertPath()); err != nil {
		t.Fatalf("read CA cert: %v", err)
	}
	if _, err := readCertPEM(mgr.ServerCertPath()); err != nil {
		t.Fatalf("read server cert: %v", err)
	}
}

func TestInit_RefusesToOverwrite(t *testing.T) {
	dataDir := t.TempDir()
	mgr := NewManager(dataDir)
	if err := mgr.Init([]string{"10.0.0.5"}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := mgr.Init([]string{"10.0.0.5"}); err == nil {
		t.Fatalf("expected second Init to fail")
	}
}

func TestIssueClient_RegistersFingerprint(t *testing.T) {
	dataDir := t.TempDir()
	mgr := NewManager(dataDir)
	if err := mgr.Init([]string{"10.0.0.5"}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	s := newTestStore(t)

	bundlePEM, err := mgr.IssueClient("ci-lss-web", s)
	if err != nil {
		t.Fatalf("IssueClient: %v", err)
	}
	if len(bundlePEM) == 0 {
		t.Fatalf("expected a non-empty PEM bundle")
	}

	certs, err := List(s)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(certs) != 1 {
		t.Fatalf("expected 1 registered certificate, got %d", len(certs))
	}
	if certs[0].Name != "ci-lss-web" {
		t.Fatalf("expected name ci-lss-web, got %s", certs[0].Name)
	}
	if certs[0].Revoked() {
		t.Fatalf("expected a freshly issued certificate to not be revoked")
	}
}

func TestRevoke(t *testing.T) {
	dataDir := t.TempDir()
	mgr := NewManager(dataDir)
	if err := mgr.Init([]string{"10.0.0.5"}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	s := newTestStore(t)
	if _, err := mgr.IssueClient("ci-lss-web", s); err != nil {
		t.Fatalf("IssueClient: %v", err)
	}

	certs, _ := List(s)
	fp := certs[0].Fingerprint

	if err := Revoke(s, fp); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	certs, _ = List(s)
	if !certs[0].Revoked() {
		t.Fatalf("expected certificate to be revoked")
	}
}

func TestReissueServer(t *testing.T) {
	dataDir := t.TempDir()
	mgr := NewManager(dataDir)
	if err := mgr.Init([]string{"10.0.0.5"}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	before, err := readCertPEM(mgr.ServerCertPath())
	if err != nil {
		t.Fatalf("read server cert: %v", err)
	}
	caBefore, err := os.ReadFile(mgr.CACertPath())
	if err != nil {
		t.Fatalf("read CA cert: %v", err)
	}

	if err := mgr.ReissueServer([]string{"10.0.0.6", "10.0.0.7"}); err != nil {
		t.Fatalf("ReissueServer: %v", err)
	}
	after, err := readCertPEM(mgr.ServerCertPath())
	if err != nil {
		t.Fatalf("read reissued server cert: %v", err)
	}
	if before.SerialNumber.Cmp(after.SerialNumber) == 0 {
		t.Fatalf("expected reissued certificate to have a different serial number")
	}
	if got := ipStrings(after.IPAddresses); !slices.Equal(got, []string{"10.0.0.6", "10.0.0.7"}) {
		t.Fatalf("expected reissued cert SAN to be [10.0.0.6 10.0.0.7], got %v", got)
	}
	caAfter, err := os.ReadFile(mgr.CACertPath())
	if err != nil {
		t.Fatalf("read CA cert after reissue: %v", err)
	}
	if !bytes.Equal(caBefore, caAfter) {
		t.Fatalf("expected ReissueServer to leave the CA certificate untouched")
	}
}

func TestInit_MultipleIPs(t *testing.T) {
	mgr := NewManager(t.TempDir())
	if err := mgr.Init([]string{"10.20.30.40", "10.20.30.41"}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	cert, err := readCertPEM(mgr.ServerCertPath())
	if err != nil {
		t.Fatalf("read server cert: %v", err)
	}
	if got := ipStrings(cert.IPAddresses); !slices.Equal(got, []string{"10.20.30.40", "10.20.30.41"}) {
		t.Fatalf("expected server cert SAN to be [10.20.30.40 10.20.30.41], got %v", got)
	}
}

func TestInit_InvalidIPLeavesNoCA(t *testing.T) {
	mgr := NewManager(t.TempDir())
	if err := mgr.Init([]string{"10.0.0.5", "10.0.0.256"}); err == nil {
		t.Fatalf("expected Init to fail for an invalid IP")
	}
	if mgr.Initialized() {
		t.Fatalf("expected no CA to be written when the IP list is invalid")
	}
}

func TestInit_IPAndDNSName(t *testing.T) {
	mgr := NewManager(t.TempDir())
	if err := mgr.Init([]string{"10.0.0.5", "agent.example.com", "AGENT.EXAMPLE.COM"}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	cert, err := readCertPEM(mgr.ServerCertPath())
	if err != nil {
		t.Fatalf("read server cert: %v", err)
	}
	if got := ipStrings(cert.IPAddresses); !slices.Equal(got, []string{"10.0.0.5"}) {
		t.Fatalf("IP SANs = %v", got)
	}
	if !slices.Equal(cert.DNSNames, []string{"agent.example.com"}) {
		t.Fatalf("DNS SANs = %v", cert.DNSNames)
	}

	caCert, err := readCertPEM(mgr.CACertPath())
	if err != nil {
		t.Fatalf("read CA cert: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	opts := x509.VerifyOptions{
		Roots:     pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, name := range []string{"10.0.0.5", "agent.example.com"} {
		opts.DNSName = name
		if _, err := cert.Verify(opts); err != nil {
			t.Fatalf("Verify %s: %v", name, err)
		}
	}
	opts.DNSName = "other.example"
	if _, err := cert.Verify(opts); err == nil {
		t.Fatalf("expected a name that is not on the certificate to fail")
	}

	ips, dns, err := mgr.ServerNames()
	if err != nil {
		t.Fatalf("ServerNames: %v", err)
	}
	if !slices.Equal(ips, []string{"10.0.0.5"}) || !slices.Equal(dns, []string{"agent.example.com"}) {
		t.Fatalf("ServerNames = %v %v", ips, dns)
	}
}

func Test_parseServerNames(t *testing.T) {
	tests := []struct {
		name    string
		ips     []string
		want    []string
		wantErr bool
	}{
		{
			name: "single IP",
			ips:  []string{"10.0.0.5"},
			want: []string{"10.0.0.5"},
		},
		{
			name: "multiple IPs keep order",
			ips:  []string{"10.0.0.6", "10.0.0.5"},
			want: []string{"10.0.0.6", "10.0.0.5"},
		},
		{
			name: "duplicates removed",
			ips:  []string{"10.0.0.5", "10.0.0.6", "10.0.0.5"},
			want: []string{"10.0.0.5", "10.0.0.6"},
		},
		{
			name: "IPv6",
			ips:  []string{"10.0.0.5", "fd00::1"},
			want: []string{"10.0.0.5", "fd00::1"},
		},
		{
			name:    "empty list",
			ips:     nil,
			wantErr: true,
		},
		{
			name:    "invalid IP",
			ips:     []string{"10.0.0.5", "10.0.0.256"},
			wantErr: true,
		},
		{
			name:    "DNS name without an IP",
			ips:     []string{"agent.example.com"},
			wantErr: true,
		},
		{
			name:    "invalid DNS name",
			ips:     []string{"10.0.0.5", "bad host"},
			wantErr: true,
		},
		{
			name: "IP and DNS name",
			ips:  []string{"agent.example.com", "10.0.0.6", "10.0.0.5", "agent.internal"},
			want: []string{"10.0.0.6", "10.0.0.5"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, dns, err := parseServerNames(tt.ips)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %v %v", got, dns)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseServerNames: %v", err)
			}
			if !slices.Equal(ipStrings(got), tt.want) {
				t.Fatalf("expected %v, got %v (dns %v)", tt.want, ipStrings(got), dns)
			}
			if tt.name == "IP and DNS name" && !slices.Equal(dns, []string{"agent.example.com", "agent.internal"}) {
				t.Fatalf("DNS names = %v", dns)
			}
		})
	}
}

func TestServerIPs(t *testing.T) {
	mgr := NewManager(t.TempDir())
	if _, err := mgr.ServerIPs(); err == nil {
		t.Fatalf("expected error before Init")
	}
	if err := mgr.Init([]string{"10.0.0.5", "10.0.0.6"}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	got, err := mgr.ServerIPs()
	if err != nil {
		t.Fatalf("ServerIPs: %v", err)
	}
	if want := []string{"10.0.0.5", "10.0.0.6"}; !slices.Equal(got, want) {
		t.Fatalf("ServerIPs = %v, want %v", got, want)
	}

	if err := mgr.ReissueServer([]string{"10.0.0.7"}); err != nil {
		t.Fatalf("ReissueServer: %v", err)
	}
	got, err = mgr.ServerIPs()
	if err != nil {
		t.Fatalf("ServerIPs after reissue: %v", err)
	}
	if want := []string{"10.0.0.7"}; !slices.Equal(got, want) {
		t.Fatalf("ServerIPs after reissue = %v, want %v", got, want)
	}
}

func ipStrings(ips []net.IP) []string {
	s := make([]string, 0, len(ips))
	for _, ip := range ips {
		s = append(s, ip.String())
	}
	return s
}
