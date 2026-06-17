// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package control

import (
	"net/http"
	"net/http/cookiejar"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildControlWebSocketURL(t *testing.T) {
	wsURL, err := buildControlWebSocketURL("https://example.com/api", "tunnel/id")
	require.NoError(t, err)
	assert.Equal(t, "wss://example.com/ws?watch=tunnel%2Fid", wsURL)
}

func TestWSDialHeaders_BearerAndCookies(t *testing.T) {
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	client := &http.Client{Jar: jar}
	serverURL := "http://127.0.0.1:8080"

	h := wsDialHeaders(client, serverURL, "tok-123")
	assert.Equal(t, "Bearer tok-123", h.Get("Authorization"))
}
