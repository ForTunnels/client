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

// Validate ensures CLI configuration is consistent. It exits the process on fatal errors.
func Validate(cfg *Config) {
	validateProtocolFlag(cfg.Protocol)
	validateServerURLFlag(cfg.ServerURL, cfg.ServerFlagProvided, cfg.AllowInsecureHTTP)
	validateTargetAddressIfNeeded(cfg)
	validateParallelAndBackoff(cfg)
	enforceEncryptionRequirements(cfg)
	validateTCPListenAddress(cfg)
	warnOnSensitiveFlagUsage(cfg)
}

func validateProtocolFlag(protocol string) {
	switch strings.ToLower(protocol) {
	case protoHTTP, protoHTTPS, protoTCP, protoUDP:
	default:
		fmt.Printf("❌ unsupported protocol: %s\n", protocol)
		fmt.Println("   Supported: http, https, tcp, udp")
		os.Exit(2)
	}
}

func validateServerURLFlag(serverURL string, serverFlagProvided, allowInsecureHTTP bool) {
	if serverFlagProvided &&
		!strings.HasPrefix(serverURL, "http://") &&
		!strings.HasPrefix(serverURL, "https://") {
		fmt.Println("❌ missing protocol in --server (use http:// or https://)")
		fmt.Println("   Example: --server http://127.0.0.1:8080")
		os.Exit(2)
	}
	u, err := url.Parse(serverURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		fmt.Println("❌ invalid server URL")
		fmt.Println("   Try: --server http://127.0.0.1:8080")
		os.Exit(2)
	}
	if strings.EqualFold(u.Scheme, "http") && !allowInsecureHTTP && !isLocalServerHost(u.Host) {
		fmt.Println("❌ insecure HTTP server URL is blocked")
		fmt.Println("   Use https:// or pass --allow-insecure-http for non-local HTTP")
		os.Exit(2)
	}
}

func validateTargetAddressIfNeeded(cfg *Config) {
	if cfg.Protocol != protoHTTP && cfg.Protocol != protoHTTPS && cfg.Protocol != protoTCP {
		return
	}
	validateTargetAddress(cfg.TargetAddr)
}

func validateTargetAddress(addr string) {
	if addr == "" || !support.LooksLikeHostPort(addr) {
		fmt.Println("❌ invalid target address")
		fmt.Println("   Expected format host:port, e.g. 127.0.0.1:8000")
		fmt.Println("   Make sure the address is correct and reachable")
		os.Exit(2)
	}
	host, portStr, err := net.SplitHostPort(addr)
	_ = host
	if err != nil {
		fmt.Println("❌ invalid target address")
		fmt.Println("   Example: 127.0.0.1:8000")
		os.Exit(2)
	}
	if pnum, e := strconv.Atoi(portStr); e != nil || pnum <= 0 || pnum > 65535 {
		fmt.Println("❌ invalid port")
		fmt.Println("   Valid range: 1-65535")
		os.Exit(2)
	}
}

func validateParallelAndBackoff(cfg *Config) {
	if cfg.Protocol == protoTCP && cfg.Parallel <= 0 {
		fmt.Println("❌ invalid parallel count")
		fmt.Println("   Use --parallel 1 or more")
		os.Exit(2)
	}
	if cfg.BackoffInitial <= 0 || cfg.BackoffMax <= 0 || cfg.BackoffMax < cfg.BackoffInitial {
		fmt.Println("❌ invalid backoff values")
		fmt.Println("   Ensure --backoff-initial > 0 and --backoff-max >= --backoff-initial")
		os.Exit(2)
	}
}

func enforceEncryptionRequirements(cfg *Config) {
	if !cfg.Encrypt {
		return
	}
	psk := strings.TrimSpace(cfg.PSK)
	if psk == "" {
		fmt.Println("❌ empty PSK")
		fmt.Println("   Provide a non-empty --psk when using --encrypt")
		os.Exit(2)
	}
	if len(psk) < 32 {
		fmt.Println("❌ PSK is too short")
		fmt.Println("   Use at least 32 characters for --psk")
		os.Exit(2)
	}
}

func validateTCPListenAddress(cfg *Config) {
	if cfg.Protocol != protoTCP || cfg.Listen == "" {
		return
	}
	if _, err := net.ResolveTCPAddr("tcp", cfg.Listen); err != nil {
		fmt.Println("❌ invalid listen address")
		fmt.Println("   Example: --listen :4000 or --listen 127.0.0.1:4000")
		os.Exit(2)
	}
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
