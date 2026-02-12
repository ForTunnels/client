// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestComputeDataPlaneAuth(t *testing.T) {
	tests := []struct {
		name         string
		tunnelID     string
		dpAuthToken  string
		dpAuthSecret string
		expected     string
	}{
		{"precomputed token", "tunnel-123", "abc123", "", "abc123"},
		{"computed from secret", "tunnel-123", "", "my-secret", ""},
		{"empty", "tunnel-123", "", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ComputeDataPlaneAuth(tt.tunnelID, tt.dpAuthToken, tt.dpAuthSecret)
			switch {
			case tt.dpAuthToken != "":
				if result != tt.dpAuthToken {
					t.Errorf("ComputeDataPlaneAuth() = %q, want %q", result, tt.dpAuthToken)
				}
			case tt.dpAuthSecret != "":
				expected := computeHMAC(tt.dpAuthSecret, tt.tunnelID)
				if result != expected {
					t.Errorf("ComputeDataPlaneAuth() = %q, want %q", result, expected)
				}
			default:
				if result != "" {
					t.Errorf("ComputeDataPlaneAuth() = %q, want empty", result)
				}
			}
		})
	}
}

func TestComputeHMAC(t *testing.T) {
	secret := "test-secret"
	message := "test-message"

	result := computeHMAC(secret, message)

	// Verify it's a valid hex string
	_, err := hex.DecodeString(result)
	if err != nil {
		t.Errorf("computeHMAC() returned invalid hex: %v", err)
	}

	// Verify it matches manual computation
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(message))
	expected := hex.EncodeToString(h.Sum(nil))

	if result != expected {
		t.Errorf("computeHMAC() = %q, want %q", result, expected)
	}

	// Test consistency
	result2 := computeHMAC(secret, message)
	if result != result2 {
		t.Errorf("computeHMAC() should be consistent, got %q and %q", result, result2)
	}
}
