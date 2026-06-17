// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateProtocolFlag(t *testing.T) {
	tests := []struct {
		name     string
		protocol string
		wantErr  bool
	}{
		{"valid http", protoHTTP, false},
		{"valid https", protoHTTPS, false},
		{"valid tcp", protoTCP, false},
		{"valid udp", protoUDP, false},
		{"invalid", "invalid", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateProtocolFlag(tt.protocol)
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), "unsupported protocol")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestEnforceEncryptionRequirements(t *testing.T) {
	tests := []struct {
		name    string
		encrypt bool
		psk     string
		wantErr bool
	}{
		{"encrypt with PSK", true, "12345678901234567890123456789012", false},
		{"encrypt without PSK", true, "", true},
		{"encrypt with empty PSK", true, "   ", true},
		{"encrypt with short PSK", true, "short", true},
		{"no encrypt", false, "", false},
		{"no encrypt with PSK", false, "short-key", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Encrypt: tt.encrypt,
				PSK:     tt.psk,
			}
			err := enforceEncryptionRequirements(cfg)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateServerURLFlag(t *testing.T) {
	tests := []struct {
		name               string
		serverURL          string
		serverFlagProvided bool
		allowInsecureHTTP  bool
		wantErr            bool
	}{
		{"default https", "https://example.com", false, false, false},
		{"local http", "http://127.0.0.1:8080", true, false, false},
		{"missing protocol", "127.0.0.1:8080", true, false, true},
		{"invalid url", "http://", true, false, true},
		{"remote http blocked", "http://example.com", true, false, true},
		{"remote http allowed", "http://example.com", true, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateServerURLFlag(tt.serverURL, tt.serverFlagProvided, tt.allowInsecureHTTP)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateTargetAddress(t *testing.T) {
	tests := []struct {
		name    string
		addr    string
		wantErr bool
	}{
		{"valid", "127.0.0.1:8000", false},
		{"empty", "", true},
		{"invalid port", "127.0.0.1:0", true},
		{"bad format", "bad", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTargetAddress(tt.addr)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateSuccess(t *testing.T) {
	cfg := &Config{
		Protocol:   protoHTTP,
		ServerURL:  "https://example.com",
		TargetAddr: "127.0.0.1:8000",
	}
	require.NoError(t, Validate(cfg))
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

func TestValidateTargetAddressIfNeeded_TCPUsesTargetAddr(t *testing.T) {
	cfg := &Config{Protocol: protoTCP, TargetAddr: "127.0.0.1:5433"}
	require.NoError(t, validateTargetAddressIfNeeded(cfg))
}

func TestValidateLoginRequiresPassword(t *testing.T) {
	tests := []struct {
		name     string
		login    string
		password string
		token    string
		wantErr  bool
	}{
		{
			name:     "login without password fails",
			login:    "user",
			password: "",
			token:    "",
			wantErr:  true,
		},
		{
			name:     "login with password passes",
			login:    "user",
			password: "secret",
			token:    "",
			wantErr:  false,
		},
		{
			name:     "login with token passes (token auth takes precedence)",
			login:    "user",
			password: "",
			token:    "bearer-token",
			wantErr:  false,
		},
		{
			name:     "no login passes",
			login:    "",
			password: "",
			token:    "",
			wantErr:  false,
		},
		{
			name:     "login with whitespace-only password fails",
			login:    "user",
			password: "   ",
			token:    "",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Login:    tt.login,
				Password: tt.password,
				Token:    tt.token,
			}
			err := validateLoginPasswordPair(cfg)
			if tt.wantErr {
				require.Error(t, err)
				require.True(t, strings.Contains(err.Error(), "password"),
					"error should mention password, got: %s", err.Error())
			} else {
				require.NoError(t, err)
			}
		})
	}
}
