// Package credential parses the Jenkins/BlueShield-style multi-line PEM
// bundle (client certificate + client private key + server CA) used to
// authenticate the deploy client against the agent's mTLS listener.
//
// Parsing decodes PEM blocks individually with pem.Decode and classifies
// each block by its type and, for certificates, by IsCA - it does not rely
// on any fixed ordering of the three sections. As a fallback for pipelines
// that collapse whitespace, the whole bundle may also be provided as a
// single line of base64.
package credential

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"strings"
)

// Bundle holds the parsed materials needed to dial the agent over mTLS.
type Bundle struct {
	ClientCert tls.Certificate
	ServerCA   *x509.CertPool
}

// TLSConfig builds a client-side tls.Config from the bundle.
func (b Bundle) TLSConfig(serverName string) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{b.ClientCert},
		RootCAs:      b.ServerCA,
		ServerName:   serverName,
		MinVersion:   tls.VersionTLS13,
	}
}

// Parse decodes raw into a Bundle. raw may be either a multi-line PEM
// bundle or, as a fallback, a single line of base64-encoded PEM bundle.
func Parse(raw []byte) (Bundle, error) {
	raw = normalize(raw)

	var (
		leafCertDER []byte
		leafCert    *x509.Certificate
		keyDER      []byte
		keyPEMType  string
		caPool      = x509.NewCertPool()
		sawCA       bool
	)

	rest := raw
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		switch {
		case block.Type == "CERTIFICATE":
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				return Bundle{}, fmt.Errorf("parse certificate block: %w", err)
			}
			if cert.IsCA {
				caPool.AddCert(cert)
				sawCA = true
			} else {
				if leafCert != nil {
					return Bundle{}, fmt.Errorf("bundle contains more than one non-CA (leaf) certificate")
				}
				leafCert = cert
				leafCertDER = block.Bytes
			}
		case strings.HasSuffix(block.Type, "PRIVATE KEY"):
			if keyDER != nil {
				return Bundle{}, fmt.Errorf("bundle contains more than one private key")
			}
			keyDER = block.Bytes
			keyPEMType = block.Type
		default:
			// Ignore unrelated block types rather than failing hard.
		}
	}

	if leafCert == nil {
		return Bundle{}, fmt.Errorf("bundle is missing a client (leaf) certificate")
	}
	if keyDER == nil {
		return Bundle{}, fmt.Errorf("bundle is missing a private key")
	}
	if !sawCA {
		return Bundle{}, fmt.Errorf("bundle is missing a server CA certificate")
	}

	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  keyPEMType,
		Bytes: keyDER,
	})
	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: leafCertDER,
	})
	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return Bundle{}, fmt.Errorf("load client key pair: %w", err)
	}

	return Bundle{
		ClientCert: tlsCert,
		ServerCA:   caPool,
	}, nil
}

// normalize detects the single-line-base64 fallback and decodes it back
// to a PEM bundle; otherwise returns raw unchanged.
func normalize(raw []byte) []byte {
	trimmed := strings.TrimSpace(string(raw))
	if strings.Contains(trimmed, "-----BEGIN") {
		return raw
	}
	decoded, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		// Not valid base64 either; return as-is so pem.Decode produces a
		// clear "missing certificate" error downstream.
		return raw
	}
	return decoded
}

// Fingerprint returns the lowercase-hex SHA-256 fingerprint of a leaf
// certificate, matching the format stored in the client_certs table and
// computed by the server-side middleware.
func Fingerprint(cert *x509.Certificate) string {
	return fingerprintDER(cert.Raw)
}
