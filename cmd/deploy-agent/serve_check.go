package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/krwu/compose-deploy-agent/internal/ca"
	"github.com/krwu/compose-deploy-agent/internal/config"
	"github.com/krwu/compose-deploy-agent/internal/tlsconf"
)

// serveConfig is everything `deploy-agent serve` loads from disk before it
// takes the instance lock.
type serveConfig struct {
	manifest     *config.Manifest
	serverCert   tls.Certificate
	clientCAPool *x509.CertPool
}

func loadServeConfig(manifestPath, dataDir string) (*serveConfig, error) {
	manifest, err := config.Load(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("load manifest: %w", err)
	}
	if err := manifest.ValidatePaths(); err != nil {
		return nil, fmt.Errorf("validate manifest paths: %w", err)
	}

	caMgr := ca.NewManager(dataDir)
	if !caMgr.Initialized() {
		return nil, fmt.Errorf("CA not initialized in %s; run `deploy-agent ca init --ip <ip>[,<ip>...]` first", dataDir)
	}
	serverCert, err := tlsconf.LoadServerCertificate(caMgr.ServerCertPath(), caMgr.ServerKeyPath())
	if err != nil {
		return nil, err
	}
	clientCAPool, err := tlsconf.LoadCAPool(caMgr.CACertPath())
	if err != nil {
		return nil, err
	}
	return &serveConfig{
		manifest:     manifest,
		serverCert:   serverCert,
		clientCAPool: clientCAPool,
	}, nil
}

// testServeConfig implements `deploy-agent serve -t`. On top of what serve
// itself needs to start, it checks things serve would only surface later as
// client handshake failures: the server certificate must be currently valid,
// chain to the local CA, and carry the --bind IP among its IP SANs.
// The returned time is the certificate's expiry; callers print the success line.
func testServeConfig(cfg *serveConfig, bindAddr string) (time.Time, error) {
	host, _, err := net.SplitHostPort(bindAddr)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid --bind %q: %w", bindAddr, err)
	}
	if net.ParseIP(host) == nil {
		return time.Time{}, fmt.Errorf("invalid --bind %q: host must be an IP address", bindAddr)
	}

	leaf := cfg.serverCert.Leaf
	if leaf == nil {
		leaf, err = x509.ParseCertificate(cfg.serverCert.Certificate[0])
		if err != nil {
			return time.Time{}, fmt.Errorf("parse server certificate: %w", err)
		}
	}
	reissueIPs := reissueSANList(leaf, host)
	now := time.Now()
	if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
		return time.Time{}, fmt.Errorf("server certificate is not valid now (valid %s to %s); run `deploy-agent ca reissue-server --ip %s`",
			leaf.NotBefore.Format(time.RFC3339), leaf.NotAfter.Format(time.RFC3339), reissueIPs)
	}
	_, err = leaf.Verify(x509.VerifyOptions{
		DNSName:     host,
		Roots:       cfg.clientCAPool,
		CurrentTime: now,
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	if err != nil {
		return time.Time{}, fmt.Errorf("server certificate does not match --bind %s or the local CA: %w; run `deploy-agent ca reissue-server --ip %s`",
			host, err, reissueIPs)
	}

	return leaf.NotAfter, nil
}

// reissueSANList builds the --ip value suggested for `ca reissue-server`:
// the names already in the certificate plus the bind host. Suggesting only
// the bind host would drop a proxy IP or a DNS name on reissue.
func reissueSANList(leaf *x509.Certificate, host string) string {
	names := make([]string, 0, len(leaf.IPAddresses)+len(leaf.DNSNames)+1)
	for _, ip := range leaf.IPAddresses {
		names = append(names, ip.String())
	}
	if bindIP := net.ParseIP(host); !slices.ContainsFunc(leaf.IPAddresses, bindIP.Equal) {
		names = append(names, bindIP.String())
	}
	names = append(names, leaf.DNSNames...)
	return strings.Join(names, ",")
}
