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
	if err := validateServerURL(cfg.ServerURL); err != nil {
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
	if err := ValidateUDPModeFlags(cfg); err != nil {
		return err
	}
	if err := ValidateUDPExposeLocalOptions(cfg); err != nil {
		return err
	}
	warnOnSensitiveFlagUsage(cfg)
	return nil
}

// validateLoginPasswordPair returns an error if --login is provided without a password.
// Password may come from --pass, --pass-file, --pass-stdin, or FORTUNNELS_PASSWORD.
func validateLoginPasswordPair(cfg *Config) error {
	if cfg.TokenFlagProvided && strings.TrimSpace(cfg.Token) != "" {
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

func validateServerURL(serverURL string) error {
	u, err := url.Parse(serverURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("invalid configured server URL")
	}

	switch strings.ToLower(u.Scheme) {
	case "https":
		return nil
	case "http":
		if isLocalServerHost(u.Hostname()) {
			return nil
		}
		return fmt.Errorf("insecure HTTP configured server URL is only allowed for local development")
	default:
		return fmt.Errorf("unsupported configured server URL scheme %q; use https://", u.Scheme)
	}
}

func isLocalServerHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	switch host {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		if strings.HasSuffix(host, ".localhost") {
			return true
		}
		// Docker Compose and other local test stacks use single-label service names.
		return !strings.Contains(host, ".")
	}
}

func validateTargetAddressIfNeeded(cfg *Config) error {
	protocol := strings.ToLower(strings.TrimSpace(cfg.Protocol))
	switch protocol {
	case protoHTTP, protoHTTPS, protoTCP:
		return validateTargetAddress(cfg.TargetAddr)
	case protoUDP:
		if IsUDPExposeLocalMode(cfg) {
			return validateTargetAddress(cfg.TargetAddr)
		}
		return nil
	default:
		return nil
	}
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
