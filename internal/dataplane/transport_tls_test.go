// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	dtls "github.com/pion/dtls/v3"
	quic "github.com/quic-go/quic-go"
)

type transportTLSFixture struct {
	certificate tls.Certificate
	caPath      string
}

func TestTransportTLS_TrustedSANCertificate(t *testing.T) {
	t.Parallel()

	fixture := newTransportTLSFixture(t)
	if err := runQUICTransportTLSHandshake(t, fixture, fixture.caPath, "127.0.0.1"); err != nil {
		t.Fatalf("trusted QUIC certificate rejected: %v", err)
	}
	if err := runDTLSTransportTLSHandshake(t, fixture, fixture.caPath, "127.0.0.1"); err != nil {
		t.Fatalf("trusted DTLS certificate rejected: %v", err)
	}
}

func TestTransportTLS_UntrustedCertificateDenied(t *testing.T) {
	t.Parallel()

	fixture := newTransportTLSFixture(t)
	if err := runQUICTransportTLSHandshake(t, fixture, "", "127.0.0.1"); err == nil {
		t.Fatal("untrusted QUIC certificate accepted")
	}
	if err := runDTLSTransportTLSHandshake(t, fixture, "", "127.0.0.1"); err == nil {
		t.Fatal("untrusted DTLS certificate accepted")
	}
}

func TestTransportTLS_WrongNameDenied(t *testing.T) {
	t.Parallel()

	fixture := newTransportTLSFixture(t)
	if err := runQUICTransportTLSHandshake(t, fixture, fixture.caPath, "wrong.example"); err == nil {
		t.Fatal("wrong-name QUIC certificate accepted")
	}
	if err := runDTLSTransportTLSHandshake(t, fixture, fixture.caPath, "wrong.example"); err == nil {
		t.Fatal("wrong-name DTLS certificate accepted")
	}
}

func TestTransportTLS_InvalidConfigurationDenied(t *testing.T) {
	t.Parallel()

	invalid := filepath.Join(t.TempDir(), "invalid-ca.pem")
	if err := os.WriteFile(invalid, []byte("not PEM"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadTransportRootCAs(invalid); err == nil {
		t.Fatal("invalid CA bundle accepted")
	}
	if _, err := loadTransportRootCAs(filepath.Join(t.TempDir(), "missing.pem")); err == nil {
		t.Fatal("missing CA bundle accepted")
	}
	if cfg, err := newTransportTLSConfig("https://127.0.0.1", ""); err != nil || cfg.ServerName != "127.0.0.1" || cfg.InsecureSkipVerify {
		t.Fatalf("default verified TLS config = %#v, %v", cfg, err)
	}
}

func newTransportTLSFixture(t *testing.T) transportTLSFixture {
	t.Helper()
	now := time.Now()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Fortunnels transport test CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
	)
	if err != nil {
		t.Fatal(err)
	}
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return transportTLSFixture{certificate: certificate, caPath: caPath}
}

func runQUICTransportTLSHandshake(t *testing.T, fixture transportTLSFixture, caPath, serverName string) error {
	t.Helper()
	listener, err := quic.ListenAddr("127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{fixture.certificate},
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{transportALPN},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	accepted := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept(ctx)
		if acceptErr == nil {
			_ = conn.CloseWithError(0, "test complete")
		}
		accepted <- acceptErr
	}()
	tlsConf, err := newTransportTLSConfig("https://"+serverName, caPath)
	if err != nil {
		return err
	}
	conn, err := quic.DialAddr(ctx, listener.Addr().String(), tlsConf, nil)
	if err == nil {
		_ = conn.CloseWithError(0, "test complete")
	}
	return err
}

func runDTLSTransportTLSHandshake(t *testing.T, fixture transportTLSFixture, caPath, serverName string) error {
	t.Helper()
	listener, err := dtls.ListenWithOptions(
		"udp",
		&net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0},
		dtls.WithCertificates(fixture.certificate),
		dtls.WithExtendedMasterSecret(dtls.RequireExtendedMasterSecret),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			acceptErr = conn.(*dtls.Conn).HandshakeContext(ctx)
			cancel()
			_ = conn.Close()
		}
		accepted <- acceptErr
	}()
	roots, err := loadTransportRootCAs(caPath)
	if err != nil {
		return err
	}
	options := []dtls.ClientOption{
		dtls.WithExtendedMasterSecret(dtls.RequireExtendedMasterSecret),
		dtls.WithServerName(serverName),
	}
	if roots != nil {
		options = append(options, dtls.WithRootCAs(roots))
	}
	udpAddr, err := net.ResolveUDPAddr("udp", listener.Addr().String())
	if err != nil {
		return err
	}
	conn, err := dtls.DialWithOptions("udp", udpAddr, options...)
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err = conn.HandshakeContext(ctx)
		cancel()
	}
	if conn != nil {
		_ = conn.Close()
	}
	select {
	case serverErr := <-accepted:
		if serverErr != nil {
			err = errors.Join(err, fmt.Errorf("DTLS server handshake: %w", serverErr))
		}
	case <-time.After(3 * time.Second):
		if err == nil {
			err = errors.New("DTLS server did not complete handshake")
		}
	}
	return err
}
