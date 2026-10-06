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
