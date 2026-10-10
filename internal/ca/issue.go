package ca

import (
	"bytes"
	"crypto/x509"
	"fmt"

	"github.com/krwu/compose-deploy-agent/internal/credential"
	"github.com/krwu/compose-deploy-agent/internal/store"
)

// IssueClient signs a new client certificate named name, registers its
// fingerprint in s, and returns the PEM bundle (client cert + client key +
// CA cert) that should be pasted into the pipeline's credential store.
// The bundle is printed once to stdout by the CLI and never persisted
// by the agent.
func (manager *Manager) IssueClient(name string, s *store.Store) (bundlePEM string, err error) {
	caKey, caCert, err := manager.loadCA()
	if err != nil {
		return "", err
	}

	key, certDER, err := generateClientLeaf(name, caKey, caCert)
	if err != nil {
		return "", fmt.Errorf("generate client leaf: %w", err)
	}
	leaf, err := x509.ParseCertificate(certDER)
	if err != nil {
		return "", fmt.Errorf("parse generated client certificate: %w", err)
	}

	fingerprint := credential.Fingerprint(leaf)
	if err := s.RegisterCert(fingerprint, name, leaf.NotBefore, leaf.NotAfter); err != nil {
		return "", fmt.Errorf("register issued certificate: %w", err)
	}

	keyPEM, err := keyPEMBytes(key)
	if err != nil {
		return "", err
	}
	caPEM, err := readFileBytes(manager.caCertPath())
	if err != nil {
		return "", fmt.Errorf("read CA certificate: %w", err)
	}

	var buf bytes.Buffer
	buf.Write(certPEMBytes(certDER))
	buf.Write(keyPEM)
	buf.Write(caPEM)
	return buf.String(), nil
}

// Revoke marks the certificate with the given fingerprint as revoked.
func Revoke(s *store.Store, fingerprint string) error {
	return s.RevokeCert(fingerprint)
}

// List returns all registered client certificates.
func List(s *store.Store) ([]store.CertRecord, error) {
	return s.ListCerts()
}

// FingerprintOf is a small convenience wrapper for CLI commands that need
// to compute a fingerprint from a certificate they already parsed.
func FingerprintOf(cert *x509.Certificate) string {
	return credential.Fingerprint(cert)
}
