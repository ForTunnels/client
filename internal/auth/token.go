// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// computeDataPlaneAuth computes data-plane authentication token if requested.
// Returns the computed token or empty string if no auth is configured.
func ComputeDataPlaneAuth(tunnelID, dpAuthTokenFlag, dpAuthSecretFlag string) string {
	if dpAuthTokenFlag != "" {
		return dpAuthTokenFlag
	}
	if strings.TrimSpace(dpAuthSecretFlag) != "" {
		return computeHMAC(dpAuthSecretFlag, tunnelID)
	}
	return ""
}

// ComputeDataPlaneAuthWithPSK is like ComputeDataPlaneAuth but falls back to psk when encrypt is true
// and no explicit dp-auth secret/token was provided (same material as stream PSK for dev setups).
func ComputeDataPlaneAuthWithPSK(tunnelID, dpAuthTokenFlag, dpAuthSecretFlag, psk string, encrypt bool) string {
	if strings.TrimSpace(dpAuthTokenFlag) != "" {
		return dpAuthTokenFlag
	}
	sec := strings.TrimSpace(dpAuthSecretFlag)
	if sec == "" && encrypt {
		sec = strings.TrimSpace(psk)
	}
	if sec == "" {
		return ""
	}
	return computeHMAC(sec, tunnelID)
}

// computeHMAC returns hex-encoded HMAC-SHA256(secret, message)
func computeHMAC(secret, message string) string {
	h := hmac.New(sha256.New, []byte(secret))
	_, _ = h.Write([]byte(message))
	return hex.EncodeToString(h.Sum(nil))
}
