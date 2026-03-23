// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package control

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRewriteIngressPublicURL(t *testing.T) {
	srv := "https://fortunnels.ru"
	assert.Equal(t, "tcp://fortunnels.ru:30000", rewriteIngressPublicURL(srv, "tcp://127.0.0.1:30000"))
	assert.Equal(t, "tcp://fortunnels.ru:30000", rewriteIngressPublicURL(srv, "tcp://0.0.0.0:30000"))
	assert.Equal(t, "udp://fortunnels.ru:10001", rewriteIngressPublicURL(srv, "udp://127.0.0.1:10001"))
	assert.Equal(t, "https://x.example/t", rewriteIngressPublicURL(srv, "https://x.example/t"))
	assert.Equal(t, "tcp://already.example:1", rewriteIngressPublicURL(srv, "tcp://already.example:1"))
	assert.Equal(t, "tcp://127.0.0.1:9", rewriteIngressPublicURL("", "tcp://127.0.0.1:9"))
	assert.Equal(t, "tcp://127.0.0.1:9", rewriteIngressPublicURL("not-a-url", "tcp://127.0.0.1:9"))
}

// TestResponseUnmarshal tests that Response struct can correctly unmarshal JSON from server
func TestResponseUnmarshal(t *testing.T) {
	// Simulate server response with user_id as number (int64)
	serverResponse := `{
		"id": "test-tunnel-123",
		"user_id": 100,
		"protocol": "http",
		"target_addr": "127.0.0.1:8080",
		"public_url": "http://test.example.com",
		"status": "active",
		"created_at": "2025-01-01T00:00:00Z",
		"last_active": "2025-01-01T00:00:00Z",
		"connections": 0,
		"is_guest": false,
		"bytes_used": 0
	}`

	var resp Response
	err := json.Unmarshal([]byte(serverResponse), &resp)
	require.NoError(t, err, "Failed to unmarshal response")
	assert.Equal(t, int64(100), resp.UserID, "Expected UserID=100")
	assert.Equal(t, "test-tunnel-123", resp.ID, "Expected ID=test-tunnel-123")
}

// TestResponseUnmarshalGuestUser tests unmarshaling with guest user (user_id=0)
func TestResponseUnmarshalGuestUser(t *testing.T) {
	serverResponse := `{
		"id": "guest-tunnel",
		"user_id": 0,
		"protocol": "http",
		"target_addr": "127.0.0.1:8080",
		"public_url": "http://test.example.com",
		"status": "active",
		"created_at": "2025-01-01T00:00:00Z",
		"last_active": "2025-01-01T00:00:00Z",
		"connections": 0,
		"is_guest": true,
		"bytes_used": 0
	}`

	var resp Response
	err := json.Unmarshal([]byte(serverResponse), &resp)
	require.NoError(t, err, "Failed to unmarshal guest response")
	assert.Equal(t, int64(0), resp.UserID, "Expected UserID=0 for guest")
	assert.True(t, resp.IsGuest, "Expected IsGuest=true")
}
