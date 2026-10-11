package main

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
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A home CA (10 years) is installed once on each parent device; the server certificate it signs
// stays under Apple's 825-day limit for trusted TLS certificates and is renewed automatically.
const (
	caLifetime     = 10 * 365 * 24 * time.Hour
	leafLifetime   = 800 * 24 * time.Hour
	leafRenewAhead = 30 * 24 * time.Hour
)

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func loadOrCreateTLS(dir string, hosts []string) (*tls.Config, error) {
	caCert, caKey, err := loadOrCreateCA(dir)
	if err != nil {
		return nil, err
	}
	leafPath, keyPath := filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key")
	cert, err := tls.LoadX509KeyPair(leafPath, keyPath)
	if err != nil || leafNeedsRenewal(cert, hosts) {
		if err := writeLeaf(leafPath, keyPath, caCert, caKey, hosts); err != nil {
			return nil, err
		}
		if cert, err = tls.LoadX509KeyPair(leafPath, keyPath); err != nil {
			return nil, err
		}
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}, nil
}

func leafNeedsRenewal(c tls.Certificate, hosts []string) bool {
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil || time.Until(leaf.NotAfter) < leafRenewAhead {
		return true
	}
	for _, h := range hosts {
		if leaf.VerifyHostname(h) != nil {
			return true
		}
	}
	return false
}

func loadOrCreateCA(dir string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certPath, keyPath := filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key")
	if certPEM, err := os.ReadFile(certPath); err == nil {
		keyPEM, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, nil, err
		}
		return parseCertKey(certPEM, keyPEM)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          randomSerial(),
		Subject:               pkix.Name{CommonName: "Homework Hours home CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(caLifetime),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	if err := writePEM(certPath, "CERTIFICATE", der, 0o644); err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	if err := writePEM(keyPath, "EC PRIVATE KEY", keyDER, 0o600); err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	return cert, key, err
}

func writeLeaf(certPath, keyPath string, ca *x509.Certificate, caKey *ecdsa.PrivateKey, hosts []string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber: randomSerial(),
		Subject:      pkix.Name{CommonName: hosts[0]},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(leafLifetime),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	if err := writePEM(keyPath, "EC PRIVATE KEY", keyDER, 0o600); err != nil {
		return err
	}
	return writePEM(certPath, "CERTIFICATE", der, 0o644)
}

func parseCertKey(certPEM, keyPEM []byte) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	cb, _ := pem.Decode(certPEM)
	kb, _ := pem.Decode(keyPEM)
	if cb == nil || kb == nil {
		return nil, nil, errors.New("bad PEM in CA files")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, nil, err
	}
	key, err := x509.ParseECPrivateKey(kb.Bytes)
	return cert, key, err
}

func writePEM(path, typ string, der []byte, mode os.FileMode) error {
	return writeFileAtomic(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), mode)
}

func randomSerial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	return n
}
