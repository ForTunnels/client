// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package config

import (
	"flag"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fortunnels/client/internal/support"
)

func TestParsePort(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"port only", "8000", "8000"},
		{"port with colon", ":9000", "9000"},
		{"invalid port", "abc", ""},
		{"empty", "", ""},
		{"host:port", "127.0.0.1:8080", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := support.ParsePort(tt.input)
			if result != tt.expected {
				t.Errorf("ParsePort(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestLooksLikeHostPort(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"valid host:port", "127.0.0.1:8080", true},
		{"valid localhost:port", "localhost:3000", true},
		{"port only", "8000", false},
		{"port with colon", ":8000", false},
		{"no port", "localhost", false},
		{"empty", "", false},
		{"invalid", "bad:value", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := support.LooksLikeHostPort(tt.input)
			if result != tt.expected {
				t.Errorf("LooksLikeHostPort(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestGetProtocolConstants(t *testing.T) {
	http, https, tcp, udp := GetProtocolConstants()
	assert.Equal(t, protoHTTP, http, "GetProtocolConstants() http = %v, want %v")
	assert.Equal(t, protoHTTPS, https, "GetProtocolConstants() https = %v, want %v")
	assert.Equal(t, protoTCP, tcp, "GetProtocolConstants() tcp = %v, want %v")
	assert.Equal(t, protoUDP, udp, "GetProtocolConstants() udp = %v, want %v")
}

func TestSetDefaultServerURL(t *testing.T) {
	original := defaultServerURL
	defer func() {
		defaultServerURL = original
	}()

	SetDefaultServerURL("https://example.com")
	if defaultServerURL != "https://example.com" {
		t.Errorf("SetDefaultServerURL() did not set defaultServerURL")
	}

	SetDefaultServerURL("")
	if defaultServerURL != "https://example.com" {
		t.Errorf("SetDefaultServerURL() should not set empty string")
	}

	SetDefaultServerURL("   ")
	if defaultServerURL != "https://example.com" {
		t.Errorf("SetDefaultServerURL() should not set whitespace-only string")
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := defaultConfig()
	if cfg.ServerURL == "" {
		t.Error("defaultConfig() ServerURL should not be empty")
	}
	if cfg.Protocol != protoHTTP {
		t.Errorf("defaultConfig() Protocol = %q, want %q", cfg.Protocol, protoHTTP)
	}
	if cfg.DataPlane != "ws" {
		t.Errorf("defaultConfig() DataPlane = %q, want ws", cfg.DataPlane)
	}
	if cfg.UserID != "default" {
		t.Errorf("defaultConfig() UserID = %q, want default", cfg.UserID)
	}
	if cfg.PSK != "" {
		t.Errorf("defaultConfig() PSK should be empty, got %q", cfg.PSK)
	}
	// TCP default mode: expose-local (serve-incoming)
}

func TestProcessPositionalArgs_TCPPort(t *testing.T) {
	protocol := "http"
	targetAddr := ""
	processPositionalArgs([]string{"tcp", "5433"}, &protocol, &targetAddr, false, false)
	assert.Equal(t, protoTCP, protocol, "tcp 5433 should set protocol to tcp")
	assert.Equal(t, "127.0.0.1:5433", targetAddr, "tcp 5433 should set target_addr to 127.0.0.1:5433")
}

func TestValidatePositionalArgs(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantErr     bool
		errContains string // when wantErr: expected substring in validatePositionalArgs error
	}{
		{"empty", nil, false, ""},
		{"single port", []string{"8000"}, false, ""},
		{"protocol then port", []string{"tcp", "5433"}, false, ""},
		{"protocol then host:port", []string{"http", "127.0.0.1:8080"}, false, ""},
		{"port then protocol swapped", []string{"5433", "tcp"}, true, "invalid argument order"},
		{"host:port then protocol swapped", []string{"127.0.0.1:8080", "http"}, true, "invalid argument order"},
		{"three positionals", []string{"tcp", "5433", "extra"}, true, "too many positional"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePositionalArgs(tt.args)
			if tt.wantErr {
				assert.Error(t, err)
				assert.ErrorContains(t, err, tt.errContains)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// testParseWithArgs runs Parse() with synthetic argv by replacing os.Args and flag.CommandLine
// (Parse also invokes normalizeArgs()). Do not use t.Parallel() in tests that call this helper:
// parallel subtests would race on those process-global values.
func testParseWithArgs(t *testing.T, args []string) (*Config, error) {
	t.Helper()
	oldArgs := os.Args
	oldFlag := flag.CommandLine
	t.Cleanup(func() {
		os.Args = oldArgs
		flag.CommandLine = oldFlag
	})
	flag.CommandLine = flag.NewFlagSet("client", flag.ContinueOnError)
	os.Args = args
	return Parse()
}

func TestParse_RejectsSwappedPositionalArgs(t *testing.T) {
	_, err := testParseWithArgs(t, []string{"client", "5433", "tcp"})
	require.Error(t, err)
	assert.ErrorContains(t, err, "invalid argument order")
}

func TestParse_AcceptsProtocolThenPort(t *testing.T) {
	cfg, err := testParseWithArgs(t, []string{"client", "tcp", "5433"})
	require.NoError(t, err)
	assert.Equal(t, protoTCP, cfg.Protocol)
	assert.Equal(t, "127.0.0.1:5433", cfg.TargetAddr)
}

func TestParse_RejectsTooManyPositionals(t *testing.T) {
	_, err := testParseWithArgs(t, []string{"client", "tcp", "5433", "extra"})
	require.Error(t, err)
	assert.ErrorContains(t, err, "too many positional")
}

func TestApplySecretSourcesFromEnv(t *testing.T) {
	t.Setenv("FORTUNNELS_TOKEN", "env-token")
	t.Setenv("FORTUNNELS_PASSWORD", "env-password")
	t.Setenv("FORTUNNELS_PSK", "env-psk")

	cfg := &Config{}
	if err := applySecretSources(cfg); err != nil {
		require.NoError(t, err, "applySecretSources() unexpected error: %v")
	}
	if cfg.Token != "env-token" {
		t.Fatalf("Token = %q, want %q", cfg.Token, "env-token")
	}
	if cfg.Password != "env-password" {
		t.Fatalf("Password = %q, want %q", cfg.Password, "env-password")
	}
	if cfg.PSK != "env-psk" {
		t.Fatalf("PSK = %q, want %q", cfg.PSK, "env-psk")
	}
}

func TestApplySecretSourcesFilePrecedence(t *testing.T) {
	t.Setenv("FORTUNNELS_TOKEN", "env-token")

	f, err := os.CreateTemp("", "token")
	require.NoError(t, err, "CreateTemp(): %v")
	defer os.Remove(f.Name())
	if _, err := f.WriteString("file-token"); err != nil {
		require.NoError(t, err, "WriteString(): %v")
	}
	if err := f.Close(); err != nil {
		require.NoError(t, err, "Close(): %v")
	}

	cfg := &Config{TokenFile: f.Name()}
	if err := applySecretSources(cfg); err != nil {
		require.NoError(t, err, "applySecretSources() unexpected error: %v")
	}
	if cfg.Token != "file-token" {
		t.Fatalf("Token = %q, want %q", cfg.Token, "file-token")
	}
}

func TestApplySecretSourcesStdinConflict(t *testing.T) {
	cfg := &Config{
		TokenFromStdin: true,
		PSKFromStdin:   true,
	}
	if err := applySecretSources(cfg); err == nil {
		t.Fatalf("applySecretSources() expected error for multiple stdin flags")
	}
}
