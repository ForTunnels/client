// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
)

const transportALPN = "fortunnels-quic"

func newTransportTLSConfig(serverURL, caPath string) (*tls.Config, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return nil, err
	}
	serverName := strings.TrimSpace(u.Hostname())
	if serverName == "" {
		return nil, fmt.Errorf("transport TLS: server URL has no hostname")
	}
	roots, err := loadTransportRootCAs(caPath)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{transportALPN},
		ServerName: serverName,
		RootCAs:    roots,
	}, nil
}

func loadTransportRootCAs(caPath string) (*x509.CertPool, error) {
	caPath = strings.TrimSpace(caPath)
	if caPath == "" {
		return nil, nil
	}
	pemData, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("transport TLS: read CA bundle: %w", err)
	}
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM(pemData) {
		return nil, fmt.Errorf("transport TLS: CA bundle contains no certificates")
	}
	return roots, nil
}

// classifyTransportDialError marks certificate verification failures as
// permanent: retrying cannot change the server certificate or the configured
// trust roots, so the reconnect loop must surface the reason instead of
// backing off forever. Other dial errors stay retryable.
func classifyTransportDialError(err error) error {
	if err == nil || !isCertificateVerificationError(err) {
		return err
	}
	return permanentError{err}
}

func isCertificateVerificationError(err error) bool {
	var verifyErr *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var hostname x509.HostnameError
	if errors.As(err, &verifyErr) || errors.As(err, &unknownAuthority) ||
		errors.As(err, &invalid) || errors.As(err, &hostname) {
		return true
	}
	// pion/dtls and the platform verifier flatten some failures into text.
	return strings.Contains(err.Error(), "x509:")
}
