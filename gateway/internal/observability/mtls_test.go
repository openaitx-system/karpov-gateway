package observability

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// generatePEM 生成自签 cert+key 写到 dir/<prefix>-cert.pem / <prefix>-key.pem。
//
// 仅用于单测：1 小时有效期，ECDSA P-256，CA bit 打开（自签自验）。
func generatePEM(t *testing.T, dir, prefix, cn string) (certPath, keyPath string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-1 * time.Minute),
		NotAfter:              time.Now().Add(1 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("createcert: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	certPath = filepath.Join(dir, prefix+"-cert.pem")
	keyPath = filepath.Join(dir, prefix+"-key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return
}

func TestLoadServerTLSConfig_OK(t *testing.T) {
	dir := t.TempDir()
	cert, key := generatePEM(t, dir, "srv", "musicgw-server")

	cfg, err := LoadServerTLSConfig(MTLSConfig{
		CertFile:     cert,
		KeyFile:      key,
		ClientCAFile: cert, // 自签：用同一份当 CA pool
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cfg.MinVersion != tls.VersionTLS13 {
		t.Errorf("min version: %d", cfg.MinVersion)
	}
	if cfg.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Errorf("client auth: %v", cfg.ClientAuth)
	}
	if len(cfg.Certificates) != 1 {
		t.Errorf("certs: %d", len(cfg.Certificates))
	}
}

func TestLoadServerTLSConfig_NoClientCA(t *testing.T) {
	dir := t.TempDir()
	cert, key := generatePEM(t, dir, "srv", "musicgw-server")

	cfg, err := LoadServerTLSConfig(MTLSConfig{CertFile: cert, KeyFile: key})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cfg.ClientAuth != tls.NoClientCert {
		t.Errorf("expected NoClientCert without ClientCAFile, got %v", cfg.ClientAuth)
	}
}

func TestLoadClientTLSConfig_OK(t *testing.T) {
	dir := t.TempDir()
	cert, key := generatePEM(t, dir, "cli", "musicgw-client")

	cfg, err := LoadClientTLSConfig(MTLSConfig{
		CertFile:   cert,
		KeyFile:    key,
		RootCAFile: cert,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cfg.RootCAs == nil {
		t.Errorf("RootCAs nil")
	}
	if cfg.InsecureSkipVerify {
		t.Errorf("InsecureSkipVerify must default false")
	}
}

func TestLoadServerTLSConfig_MissingCert(t *testing.T) {
	if _, err := LoadServerTLSConfig(MTLSConfig{}); !errors.Is(err, ErrMissingCert) {
		t.Errorf("expected ErrMissingCert, got %v", err)
	}
}

func TestLoadClientTLSConfig_MissingCert(t *testing.T) {
	if _, err := LoadClientTLSConfig(MTLSConfig{}); !errors.Is(err, ErrMissingCert) {
		t.Errorf("expected ErrMissingCert, got %v", err)
	}
}

func TestLoadServerTLSConfig_BadKeypair(t *testing.T) {
	dir := t.TempDir()
	bogus := filepath.Join(dir, "bogus.pem")
	_ = os.WriteFile(bogus, []byte("not a pem"), 0o600)

	if _, err := LoadServerTLSConfig(MTLSConfig{CertFile: bogus, KeyFile: bogus}); err == nil {
		t.Errorf("expected error for bogus PEM")
	}
}

func TestLoadServerTLSConfig_BadCAFile(t *testing.T) {
	dir := t.TempDir()
	cert, key := generatePEM(t, dir, "srv", "musicgw-server")
	bogus := filepath.Join(dir, "bogus_ca.pem")
	_ = os.WriteFile(bogus, []byte("not pem"), 0o600)

	if _, err := LoadServerTLSConfig(MTLSConfig{CertFile: cert, KeyFile: key, ClientCAFile: bogus}); err == nil {
		t.Errorf("expected error for bogus CA PEM")
	}
}

func TestLoadServerTLSConfig_CustomMinVersion(t *testing.T) {
	dir := t.TempDir()
	cert, key := generatePEM(t, dir, "srv", "musicgw-server")

	cfg, err := LoadServerTLSConfig(MTLSConfig{
		CertFile: cert, KeyFile: key, MinVersion: tls.VersionTLS12,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("min version: %d", cfg.MinVersion)
	}
}
