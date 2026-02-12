// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package support

import (
	"io"
	"net"
	"os"
	"testing"
)

func TestIsBenignCopyError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, true},
		{"EOF", io.EOF, true},
		{"UnexpectedEOF", io.ErrUnexpectedEOF, true},
		{"net.ErrClosed", net.ErrClosed, true},
		{"connection closed message", &net.OpError{Err: &os.SyscallError{Err: net.ErrClosed}}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsBenignCopyError(tt.err)
			if result != tt.expected {
				t.Errorf("IsBenignCopyError(%v) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

func TestGetDefaultServerURL(t *testing.T) {
	// Save original env
	originalEnv := os.Getenv("FORTUNNELS_SERVER_URL")
	defer func() {
		if originalEnv != "" {
			os.Setenv("FORTUNNELS_SERVER_URL", originalEnv)
		} else {
			os.Unsetenv("FORTUNNELS_SERVER_URL")
		}
	}()

	// Test with env var
	os.Setenv("FORTUNNELS_SERVER_URL", "https://custom.example.com")
	result := GetDefaultServerURL("https://default.example.com")
	if result != "https://custom.example.com" {
		t.Errorf("GetDefaultServerURL() with env = %q, want https://custom.example.com", result)
	}

	// Test without env var
	os.Unsetenv("FORTUNNELS_SERVER_URL")
	result = GetDefaultServerURL("https://default.example.com")
	if result != "https://default.example.com" {
		t.Errorf("GetDefaultServerURL() without env = %q, want https://default.example.com", result)
	}

	// Test with empty env var
	os.Setenv("FORTUNNELS_SERVER_URL", "")
	result = GetDefaultServerURL("https://default.example.com")
	if result != "https://default.example.com" {
		t.Errorf("GetDefaultServerURL() with empty env = %q, want https://default.example.com", result)
	}
}

func TestToUint32Size(t *testing.T) {
	tests := []struct {
		name    string
		input   int
		wantErr bool
	}{
		{"valid small", 100, false},
		{"valid large", 1000000, false},
		{"zero", 0, false},
		{"negative", -1, true},
		{"max uint32", 4294967295, false},
		{"over max", 4294967296, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ToUint32Size(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ToUint32Size(%d) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && result != uint32(tt.input) {
				t.Errorf("ToUint32Size(%d) = %d, want %d", tt.input, result, tt.input)
			}
		})
	}
}

func TestToUint16Size(t *testing.T) {
	tests := []struct {
		name    string
		input   int
		wantErr bool
	}{
		{"valid small", 100, false},
		{"zero", 0, false},
		{"negative", -1, true},
		{"max uint16", 65535, false},
		{"over max", 65536, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ToUint16Size(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ToUint16Size(%d) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && result != uint16(tt.input) {
				t.Errorf("ToUint16Size(%d) = %d, want %d", tt.input, result, tt.input)
			}
		})
	}
}
