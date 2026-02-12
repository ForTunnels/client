// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package config

import (
	"os"
	"testing"

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
	if http != protoHTTP {
		t.Errorf("GetProtocolConstants() http = %q, want %q", http, protoHTTP)
	}
	if https != protoHTTPS {
		t.Errorf("GetProtocolConstants() https = %q, want %q", https, protoHTTPS)
	}
	if tcp != protoTCP {
		t.Errorf("GetProtocolConstants() tcp = %q, want %q", tcp, protoTCP)
	}
	if udp != protoUDP {
		t.Errorf("GetProtocolConstants() udp = %q, want %q", udp, protoUDP)
	}
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
}

func TestApplySecretSourcesFromEnv(t *testing.T) {
	t.Setenv("FORTUNNELS_TOKEN", "env-token")
	t.Setenv("FORTUNNELS_PASSWORD", "env-password")
	t.Setenv("FORTUNNELS_PSK", "env-psk")

	cfg := &Config{}
	if err := applySecretSources(cfg); err != nil {
		t.Fatalf("applySecretSources() unexpected error: %v", err)
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
	if err != nil {
		t.Fatalf("CreateTemp(): %v", err)
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString("file-token"); err != nil {
		t.Fatalf("WriteString(): %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}

	cfg := &Config{TokenFile: f.Name()}
	if err := applySecretSources(cfg); err != nil {
		t.Fatalf("applySecretSources() unexpected error: %v", err)
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
