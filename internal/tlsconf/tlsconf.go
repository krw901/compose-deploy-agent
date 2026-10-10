// Package tlsconf builds the server-side tls.Config used by `deploy-agent
// serve`. Certificate chain and expiry validation happens here, at the TLS
// handshake layer; fingerprint registration/revocation checks happen one
// layer up, in an HTTP middleware, because a handshake failure cannot carry
// a structured JSON error body.
package tlsconf

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
)

// ServerConfig builds the tls.Config for the mTLS listener on :8866.
// clientCAPool must contain the local CA that signs client leaf
// certificates.
func ServerConfig(serverCert tls.Certificate, clientCAPool *x509.CertPool) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientCAs:    clientCAPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS13,
	}
}

// LoadServerCertificate loads a PEM-encoded certificate/key pair from disk.
func LoadServerCertificate(certPath, keyPath string) (tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("load server certificate: %w", err)
	}
	return cert, nil
}

// LoadCAPool loads a single PEM-encoded CA certificate file into a pool.
func LoadCAPool(caPath string) (*x509.CertPool, error) {
	pemBytes, err := readFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("read CA file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("no valid certificates found in %s", caPath)
	}
	return pool, nil
}
