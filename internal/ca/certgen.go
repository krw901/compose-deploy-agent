package ca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"math/big"
	"net"
	"slices"
	"strings"
	"time"
)

// keyAlgorithm is ECDSA P-256 throughout, chosen for small certificate size
// and good native Go support.
func newKey() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}

func newSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	return rand.Int(rand.Reader, limit)
}

func generateCA() (key *ecdsa.PrivateKey, cert *x509.Certificate, certDER []byte, err error) {
	key, err = newKey()
	if err != nil {
		return nil, nil, nil, err
	}
	serial, err := newSerial()
	if err != nil {
		return nil, nil, nil, err
	}

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "deploy-agent local CA"},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.Add(CAValidity),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create CA certificate: %w", err)
	}
	cert, err = x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parse generated CA certificate: %w", err)
	}
	return key, cert, der, nil
}

// parseServerNames splits names into IP and DNS SANs. Each entry is either
// an IP address or a DNS name; at least one IP is required because the
// agent binds to an IP. Duplicates are dropped, and the relative order of
// each kind is kept.
func parseServerNames(names []string) (ips []net.IP, dns []string, err error) {
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		ip, host, err := ClassifyServerName(name)
		if err != nil {
			return nil, nil, err
		}
		if ip != nil {
			if !slices.ContainsFunc(ips, ip.Equal) {
				ips = append(ips, ip)
			}
			continue
		}
		if slices.ContainsFunc(dns, func(existing string) bool {
			return strings.EqualFold(existing, host)
		}) {
			continue
		}
		dns = append(dns, host)
	}
	if len(ips) == 0 {
		return nil, nil, errors.New("at least one IP address is required")
	}
	return ips, dns, nil
}

// ClassifyServerName reports whether name belongs in the server
// certificate's IP SAN or DNS SAN. Exactly one of ip and dns is set.
func ClassifyServerName(name string) (ip net.IP, dns string, err error) {
	if ip = net.ParseIP(name); ip != nil {
		return ip, "", nil
	}
	if ipShaped(name) {
		return nil, "", fmt.Errorf("invalid IP address %q", name)
	}
	if err := validateDNSName(name); err != nil {
		return nil, "", err
	}
	return nil, name, nil
}

// ipShaped reports whether name is made only of digits, dots and colons, so
// a typo such as 10.0.0.256 stays an invalid IP instead of becoming a DNS name.
func ipShaped(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if (r < '0' || r > '9') && r != '.' && r != ':' {
			return false
		}
	}
	return true
}

func validateDNSName(name string) error {
	if name == "" || len(name) > 253 || strings.ContainsAny(name, " *\\/@") {
		return fmt.Errorf("invalid DNS name %q", name)
	}
	for label := range strings.SplitSeq(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("invalid DNS name %q", name)
		}
		for i := range len(label) {
			c := label[i]
			switch {
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
			default:
				return fmt.Errorf("invalid DNS name %q", name)
			}
		}
	}
	return nil
}

// generateServerLeaf issues a server-auth leaf certificate. names are IP
// addresses and DNS names; both are written as SANs, so a client can dial
// either. Go's TLS client verification requires a SAN match; CommonName
// alone is rejected.
func generateServerLeaf(names []string, caKeyAny any, caCert *x509.Certificate) (*ecdsa.PrivateKey, []byte, error) {
	caKey, ok := caKeyAny.(*ecdsa.PrivateKey)
	if !ok {
		return nil, nil, fmt.Errorf("unsupported CA key type %T", caKeyAny)
	}
	parsedIPs, dnsNames, err := parseServerNames(names)
	if err != nil {
		return nil, nil, err
	}

	key, err := newKey()
	if err != nil {
		return nil, nil, err
	}
	serial, err := newSerial()
	if err != nil {
		return nil, nil, err
	}

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: parsedIPs[0].String()},
		NotBefore:    now.Add(-5 * time.Minute),
		NotAfter:     now.Add(LeafValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  parsedIPs,
		DNSNames:     dnsNames,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("create server certificate: %w", err)
	}
	return key, der, nil
}

// generateClientLeaf issues a client-auth leaf certificate with name in the
// DNSNames SAN (used as a human-readable identifier, e.g. the pipeline
// name).
func generateClientLeaf(name string, caKeyAny any, caCert *x509.Certificate) (*ecdsa.PrivateKey, []byte, error) {
	caKey, ok := caKeyAny.(*ecdsa.PrivateKey)
	if !ok {
		return nil, nil, fmt.Errorf("unsupported CA key type %T", caKeyAny)
	}

	key, err := newKey()
	if err != nil {
		return nil, nil, err
	}
	serial, err := newSerial()
	if err != nil {
		return nil, nil, err
	}

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    now.Add(-5 * time.Minute),
		NotAfter:     now.Add(LeafValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		DNSNames:     []string{name},
	}

	der, err := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("create client certificate: %w", err)
	}
	return key, der, nil
}
