// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/fortunnels/client/internal/support"
)

// Validate ensures CLI configuration is consistent.
func Validate(cfg *Config) error {
	if err := validateProtocolFlag(cfg.Protocol); err != nil {
		return err
	}
	if err := validateServerURLFlag(cfg.ServerURL, cfg.ServerFlagProvided, cfg.AllowInsecureHTTP); err != nil {
		return err
	}
	if err := validateTargetAddressIfNeeded(cfg); err != nil {
		return err
	}
	if err := enforceEncryptionRequirements(cfg); err != nil {
		return err
	}
	if err := validateLoginPasswordPair(cfg); err != nil {
		return err
	}
	warnOnSensitiveFlagUsage(cfg)
	return nil
}

// validateLoginPasswordPair returns an error if --login is provided without a password.
// Password may come from --pass, --pass-file, --pass-stdin, or FORTUNNELS_PASSWORD.
func validateLoginPasswordPair(cfg *Config) error {
	if strings.TrimSpace(cfg.Token) != "" {
		return nil
	}
	if strings.TrimSpace(cfg.Login) == "" {
		return nil
	}
	if strings.TrimSpace(cfg.Password) != "" {
		return nil
	}
	return fmt.Errorf("when using --login, provide password via --pass, --pass-file, --pass-stdin, or FORTUNNELS_PASSWORD")
}

func validateProtocolFlag(protocol string) error {
	switch strings.ToLower(protocol) {
	case protoHTTP, protoHTTPS, protoTCP, protoUDP:
		return nil
	default:
		return fmt.Errorf("unsupported protocol: %s\n   Supported: http, https, tcp, udp", protocol)
	}
}

func validateServerURLFlag(serverURL string, serverFlagProvided, allowInsecureHTTP bool) error {
	if serverFlagProvided &&
		!strings.HasPrefix(serverURL, "http://") &&
		!strings.HasPrefix(serverURL, "https://") {
		return fmt.Errorf("missing protocol in --server (use http:// or https://)\n   Example: --server http://127.0.0.1:8080")
	}
	u, err := url.Parse(serverURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("invalid server URL\n   Try: --server http://127.0.0.1:8080")
	}
	if strings.EqualFold(u.Scheme, "http") && !allowInsecureHTTP && !isLocalServerHost(u.Host) {
		return fmt.Errorf("insecure HTTP server URL is blocked\n   Use https:// or pass --allow-insecure-http for non-local HTTP")
	}
	return nil
}

func validateTargetAddressIfNeeded(cfg *Config) error {
	if cfg.Protocol != protoHTTP && cfg.Protocol != protoHTTPS && cfg.Protocol != protoTCP {
		return nil
	}
	return validateTargetAddress(cfg.TargetAddr)
}

func validateTargetAddress(addr string) error {
	if addr == "" || !support.LooksLikeHostPort(addr) {
		return fmt.Errorf("invalid target address\n   Expected format host:port, e.g. 127.0.0.1:8000\n   Make sure the address is correct and reachable")
	}
	host, portStr, err := net.SplitHostPort(addr)
	_ = host
	if err != nil {
		return fmt.Errorf("invalid target address\n   Example: 127.0.0.1:8000")
	}
	if pnum, e := strconv.Atoi(portStr); e != nil || pnum <= 0 || pnum > 65535 {
		return fmt.Errorf("invalid port\n   Valid range: 1-65535")
	}
	return nil
}

func enforceEncryptionRequirements(cfg *Config) error {
	if !cfg.Encrypt {
		return nil
	}
	psk := strings.TrimSpace(cfg.PSK)
	if psk == "" {
		return fmt.Errorf("empty PSK\n   Provide a non-empty --psk when using --encrypt")
	}
	if len(psk) < 32 {
		return fmt.Errorf("PSK is too short\n   Use at least 32 characters for --psk")
	}
	return nil
}

func isLocalServerHost(host string) bool {
	if host == "" {
		return false
	}
	h := host
	if strings.HasPrefix(h, "[") && strings.Contains(h, "]") {
		h = strings.TrimPrefix(h, "[")
		h = strings.SplitN(h, "]", 2)[0]
	}
	if strings.Contains(h, ":") {
		if splitHost, _, err := net.SplitHostPort(h); err == nil {
			h = splitHost
		}
	}
	switch strings.ToLower(h) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func warnOnSensitiveFlagUsage(cfg *Config) {
	type secretFlag struct {
		label string
		used  bool
		value string
	}
	entries := []secretFlag{
		{label: "--token", used: cfg.TokenFlagProvided, value: cfg.Token},
		{label: "--pass", used: cfg.PasswordFlagProvided, value: cfg.Password},
		{label: "--psk", used: cfg.PSKFlagProvided, value: cfg.PSK},
		{label: "--dp-auth-token", used: cfg.DPAuthTokenFlagProvided, value: cfg.DPAuthToken},
		{label: "--dp-auth-secret", used: cfg.DPAuthSecretFlagProvided, value: cfg.DPAuthSecret},
	}
	for _, entry := range entries {
		if entry.used && strings.TrimSpace(entry.value) != "" {
			fmt.Fprintf(os.Stderr, "⚠️  %s was provided via CLI and may be visible in process listings\n", entry.label)
		}
	}
}
