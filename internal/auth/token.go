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

// computeHMAC returns hex-encoded HMAC-SHA256(secret, message)
func computeHMAC(secret, message string) string {
	h := hmac.New(sha256.New, []byte(secret))
	_, _ = h.Write([]byte(message))
	return hex.EncodeToString(h.Sum(nil))
}
