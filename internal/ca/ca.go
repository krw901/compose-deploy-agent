// Package ca implements the local, per-machine certificate authority.
// Every agent installation self-signs its own CA and trusts only
// certificates it issued itself - there is no cross-machine shared CA.
// Signing is only ever exposed through the CLI subcommands (`ca init`,
// `ca issue`, ...), never over the HTTP listener, so that a leaked client
// certificate can never be used to mint further certificates.
package ca

import (
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/krwu/compose-deploy-agent/internal/store"
)

// CAValidity is the lifetime of the self-signed root, chosen to avoid a
// whole-machine re-trust operation.
const CAValidity = 10 * 365 * 24 * time.Hour

// LeafValidity is the lifetime of both client and server leaf certificates,
// balancing rotation frequency against the exposure window if leaked.
const LeafValidity = 365 * 24 * time.Hour

// ExpiryWarningWindow is how far ahead of expiry the agent starts logging
// warnings.
const ExpiryWarningWindow = 30 * 24 * time.Hour

// Manager owns the on-disk CA material rooted at dataDir/ca and
// dataDir/server.{crt,key}.
type Manager struct {
	dataDir string
}

// NewManager returns a Manager rooted at dataDir (typically
// /var/lib/deploy-agent).
func NewManager(dataDir string) *Manager {
	return &Manager{dataDir: dataDir}
}

func (m *Manager) caDir() string          { return filepath.Join(m.dataDir, "ca") }
func (m *Manager) caKeyPath() string      { return filepath.Join(m.caDir(), "ca.key") }
func (m *Manager) caCertPath() string     { return filepath.Join(m.caDir(), "ca.crt") }
func (m *Manager) serverKeyPath() string  { return filepath.Join(m.dataDir, "server.key") }
func (m *Manager) serverCertPath() string { return filepath.Join(m.dataDir, "server.crt") }

// ServerKeyPath returns the path to the server leaf's private key.
func (m *Manager) ServerKeyPath() string { return m.serverKeyPath() }

// ServerCertPath returns the path to the server leaf's certificate.
func (m *Manager) ServerCertPath() string { return m.serverCertPath() }

// CACertPath returns the path to the CA certificate, which is also the
// trust anchor for verifying client certificates on the mTLS listener.
func (m *Manager) CACertPath() string { return m.caCertPath() }

// Initialized reports whether `ca init` has already run.
func (m *Manager) Initialized() bool {
	_, err := os.Stat(m.caKeyPath())
	return err == nil
}

// Init generates the local CA and an initial server leaf certificate
// covering every IP and DNS name in names. It fails if the CA already
// exists, to avoid silently invalidating every certificate issued so far.
func (manager *Manager) Init(names []string) error {
	if manager.Initialized() {
		return fmt.Errorf("CA already initialized at %s; refusing to overwrite", manager.caDir())
	}
	// Validate before writing the CA, otherwise a typo would leave a CA
	// without a server certificate that Init then refuses to overwrite.
	if _, _, err := parseServerNames(names); err != nil {
		return err
	}
	if err := os.MkdirAll(manager.caDir(), 0o700); err != nil {
		return fmt.Errorf("create CA dir: %w", err)
	}

	caKey, caCert, caCertDER, err := generateCA()
	if err != nil {
		return fmt.Errorf("generate CA: %w", err)
	}
	if err := writeKeyPEM(manager.caKeyPath(), caKey, 0o600); err != nil {
		return err
	}
	if err := writeCertPEM(manager.caCertPath(), caCertDER, 0o644); err != nil {
		return err
	}

	if err := manager.issueServerCert(names, caKey, caCert); err != nil {
		return fmt.Errorf("issue initial server certificate: %w", err)
	}
	return nil
}

// ReissueServer signs a fresh server leaf certificate covering every IP
// and DNS name in names using the existing CA, overwriting the previous
// one. Used both for routine renewal and when the agent's reachable
// addresses change. The CA itself is left untouched, so issued client
// certificates stay valid.
func (m *Manager) ReissueServer(names []string) error {
	caKey, caCert, err := m.loadCA()
	if err != nil {
		return err
	}
	return m.issueServerCert(names, caKey, caCert)
}

// ServerNames returns the IP SANs and DNS SANs of the current server certificate.
func (m *Manager) ServerNames() (ips, dns []string, err error) {
	cert, err := readCertPEM(m.serverCertPath())
	if err != nil {
		return nil, nil, fmt.Errorf("read server cert: %w", err)
	}
	ips = make([]string, 0, len(cert.IPAddresses))
	for _, ip := range cert.IPAddresses {
		ips = append(ips, ip.String())
	}
	return ips, cert.DNSNames, nil
}

// ServerIPs returns the IP SANs of the current server certificate.
func (m *Manager) ServerIPs() ([]string, error) {
	ips, _, err := m.ServerNames()
	return ips, err
}

func (m *Manager) issueServerCert(names []string, caKey any, caCert *x509.Certificate) error {
	key, certDER, err := generateServerLeaf(names, caKey, caCert)
	if err != nil {
		return fmt.Errorf("generate server leaf: %w", err)
	}
	if err := writeKeyPEM(m.serverKeyPath(), key, 0o600); err != nil {
		return err
	}
	return writeCertPEM(m.serverCertPath(), certDER, 0o644)
}

func (m *Manager) loadCA() (any, *x509.Certificate, error) {
	key, err := readKeyPEM(m.caKeyPath())
	if err != nil {
		return nil, nil, fmt.Errorf("read CA key: %w", err)
	}
	cert, err := readCertPEM(m.caCertPath())
	if err != nil {
		return nil, nil, fmt.Errorf("read CA cert: %w", err)
	}
	return key, cert, nil
}

// ExpiringCerts returns registered client certificates expiring within
// ExpiryWarningWindow, for the startup and daily expiry warning.
func ExpiringCerts(s *store.Store) ([]store.CertRecord, error) {
	return s.ExpiringWithin(ExpiryWarningWindow)
}
