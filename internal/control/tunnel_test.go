// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package control

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
