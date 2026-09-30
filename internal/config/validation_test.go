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

func TestValidateServerURL(t *testing.T) {
	tests := []struct {
		name      string
		serverURL string
		wantErr   bool
	}{
		{"configured https", "https://fortunnels.ru", false},
		{"configured remote https", "https://staging.fortunnels.ru", false},
		{"configured local http for tests", "http://127.0.0.1:8080", false},
		{"configured localhost http for tests", "http://localhost:8080", false},
		{"configured local admin http for tests", "http://admin.localhost:8080", false},
		{"configured IPv6 localhost http for tests", "http://[::1]:8080", false},
		{"configured docker service http for tests", "http://server:8080", false},
		{"missing protocol", "127.0.0.1:8080", true},
		{"invalid url", "http://", true},
		{"remote http rejected", "http://staging.fortunnels.ru", true},
		{"local-looking remote http rejected", "http://admin.localhost.example.com", true},
		{"unsupported scheme rejected", "ftp://fortunnels.ru", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateServerURL(tt.serverURL)
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
	tests := map[string]bool{
		"localhost":                        true,
		"LOCALHOST":                        true,
		"localhost.":                       true,
		"fortunnels.localhost":             true,
		"admin.fortunnels.localhost":       true,
		"127.0.0.1":                        true,
		"::1":                              true,
		"server":                           true,
		"postgres":                         true,
		"staging.fortunnels.ru":            false,
		"fortunnels.localhost.example.com": false,
		"127.0.0.2":                        false,
	}
	for host, want := range tests {
		require.Equal(t, want, isLocalServerHost(host), "isLocalServerHost(%q)", host)
	}
}

func TestValidateTargetAddressIfNeeded_TCPUsesTargetAddr(t *testing.T) {
	cfg := &Config{Protocol: protoTCP, TargetAddr: "127.0.0.1:5433"}
	require.NoError(t, validateTargetAddressIfNeeded(cfg))
}

func TestValidateLoginRequiresPassword(t *testing.T) {
	tests := []struct {
		name              string
		login             string
		password          string
		token             string
		tokenFlagProvided bool
		wantErr           bool
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
			name:              "explicit token flag skips login password requirement",
			login:             "user",
			password:          "",
			token:             "bearer-token",
			tokenFlagProvided: true,
			wantErr:           false,
		},
		{
			name:     "config token without flag still requires password with login",
			login:    "user",
			password: "",
			token:    "bearer-token",
			wantErr:  true,
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
				Login:             tt.login,
				Password:          tt.password,
				Token:             tt.token,
				TokenFlagProvided: tt.tokenFlagProvided,
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
