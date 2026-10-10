package credential

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

// testBundle generates a throwaway CA + client leaf pair and returns the
// three PEM blocks in the given order, matching the scenarios credential
// parsing must tolerate (order-independent).
func testBundleBlocks(t *testing.T) (certPEM, keyPEM, caPEM []byte) {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}

	clientKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}
	clientTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "test-client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		DNSNames:     []string{"test-client"},
	}
	clientDER, err := x509.CreateCertificate(rand.Reader, clientTemplate, caCert, &clientKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create client cert: %v", err)
	}
	clientKeyDER, err := x509.MarshalECPrivateKey(clientKey)
	if err != nil {
		t.Fatalf("marshal client key: %v", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: clientDER,
	})
	keyPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: clientKeyDER,
	})
	caPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caDER,
	})
	return certPEM, keyPEM, caPEM
}

func TestParse_StandardOrder(t *testing.T) {
	certPEM, keyPEM, caPEM := testBundleBlocks(t)
	bundle := bytes.Join([][]byte{certPEM, keyPEM, caPEM}, nil)

	b, err := Parse(bundle)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(b.ClientCert.Certificate) == 0 {
		t.Fatalf("expected a client certificate to be loaded")
	}
	if b.ServerCA == nil {
		t.Fatalf("expected a CA pool")
	}
}

func TestParse_OrderIndependent(t *testing.T) {
	certPEM, keyPEM, caPEM := testBundleBlocks(t)
	// CA first, then key, then cert - parsing must not depend on order.
	bundle := bytes.Join([][]byte{caPEM, keyPEM, certPEM}, nil)

	if _, err := Parse(bundle); err != nil {
		t.Fatalf("Parse should tolerate any block order: %v", err)
	}
}

func TestParse_Base64Fallback(t *testing.T) {
	certPEM, keyPEM, caPEM := testBundleBlocks(t)
	bundle := bytes.Join([][]byte{certPEM, keyPEM, caPEM}, nil)
	encoded := []byte(base64.StdEncoding.EncodeToString(bundle))

	b, err := Parse(encoded)
	if err != nil {
		t.Fatalf("Parse (base64 fallback): %v", err)
	}
	if len(b.ClientCert.Certificate) == 0 {
		t.Fatalf("expected a client certificate to be loaded")
	}
}

func TestParse_MissingPiecesFail(t *testing.T) {
	certPEM, keyPEM, caPEM := testBundleBlocks(t)

	cases := map[string][]byte{
		"missing key": bytes.Join([][]byte{certPEM, caPEM}, nil),
		"missing ca":  bytes.Join([][]byte{certPEM, keyPEM}, nil),
		"missing all": []byte("not a pem bundle at all"),
	}
	for name, bundle := range cases {
		if _, err := Parse(bundle); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestFingerprint_StableForSameCert(t *testing.T) {
	certPEM, _, _ := testBundleBlocks(t)
	block, _ := pem.Decode(certPEM)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}

	fp1 := Fingerprint(cert)
	fp2 := Fingerprint(cert)
	if fp1 != fp2 || fp1 == "" {
		t.Fatalf("expected stable non-empty fingerprint, got %q and %q", fp1, fp2)
	}
}
