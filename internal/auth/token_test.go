// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestComputeDataPlaneAuthWithPSK_Fallback(t *testing.T) {
	tid := "t-1"
	got := ComputeDataPlaneAuthWithPSK(tid, "", "", "psk-fallback-psk-fallback-psk", true)
	want := computeHMAC("psk-fallback-psk-fallback-psk", tid)
	assert.Equal(t, want, got)
	assert.Empty(t, ComputeDataPlaneAuthWithPSK(tid, "", "", "", true))
}

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
				assert.Equal(t, tt.dpAuthToken, result, "ComputeDataPlaneAuth() = %v, want %v")
			case tt.dpAuthSecret != "":
				expected := computeHMAC(tt.dpAuthSecret, tt.tunnelID)
				assert.Equal(t, expected, result, "ComputeDataPlaneAuth() = %v, want %v")
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

	assert.Equal(t, expected, result, "computeHMAC() = %v, want %v")

	// Test consistency
	result2 := computeHMAC(secret, message)
	assert.Equal(t, result2, result, "computeHMAC() should be consistent, got %v and %v")
}
