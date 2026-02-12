// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package config

import (
	"testing"
)

func TestValidateProtocolFlag(t *testing.T) {
	// Note: This test verifies the logic, but cannot test os.Exit behavior
	// In practice, invalid protocols will cause os.Exit(2)
	tests := []struct {
		name     string
		protocol string
		valid    bool
	}{
		{"valid http", protoHTTP, true},
		{"valid https", protoHTTPS, true},
		{"valid tcp", protoTCP, true},
		{"valid udp", protoUDP, true},
		{"invalid", "invalid", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// We can only test that valid protocols don't cause issues
			// Actual os.Exit behavior is tested in integration tests
			if tt.valid {
				// Valid protocols should not cause issues
				validateProtocolFlag(tt.protocol)
			}
		})
	}
}

func TestEnforceEncryptionRequirements(t *testing.T) {
	// Note: This test verifies the logic, but cannot test os.Exit behavior
	// In practice, invalid encryption config will cause os.Exit(2)
	tests := []struct {
		name    string
		encrypt bool
		psk     string
		valid   bool
	}{
		{"encrypt with PSK", true, "12345678901234567890123456789012", true},
		{"encrypt without PSK", true, "", false},
		{"encrypt with empty PSK", true, "   ", false},
		{"no encrypt", false, "", true},
		{"no encrypt with PSK", false, "short-key", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Encrypt: tt.encrypt,
				PSK:     tt.psk,
			}
			// We can only test that valid configs don't cause issues
			// Actual os.Exit behavior is tested in integration tests
			if tt.valid {
				enforceEncryptionRequirements(cfg)
			}
		})
	}
}

func TestIsLocalServerHost(t *testing.T) {
	tests := []struct {
		host     string
		expected bool
	}{
		{"localhost", true},
		{"localhost:8080", true},
		{"127.0.0.1:8080", true},
		{"[::1]:443", true},
		{"example.com", false},
		{"example.com:8080", false},
	}

	for _, tt := range tests {
		if got := isLocalServerHost(tt.host); got != tt.expected {
			t.Fatalf("isLocalServerHost(%q) = %v, want %v", tt.host, got, tt.expected)
		}
	}
}
