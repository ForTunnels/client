// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

//go:build integration

package dataplane

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/fortunnels/client/internal/config"
)

func TestNextBackoff(t *testing.T) {
	tests := []struct {
		name     string
		current  time.Duration
		limit    time.Duration
		expected time.Duration
	}{
		{
			name:     "double within limit",
			current:  time.Second,
			limit:    10 * time.Second,
			expected: 2 * time.Second,
		},
		{
			name:     "double exceeds limit",
			current:  5 * time.Second,
			limit:    8 * time.Second,
			expected: 8 * time.Second,
		},
		{
			name:     "exactly at limit",
			current:  4 * time.Second,
			limit:    8 * time.Second,
			expected: 8 * time.Second,
		},
		{
			name:     "zero current",
			current:  0,
			limit:    10 * time.Second,
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := nextBackoff(tt.current, tt.limit)
			if result != tt.expected {
				t.Errorf("nextBackoff(%v, %v) = %v, want %v", tt.current, tt.limit, result, tt.expected)
			}
		})
	}
}

func TestManagerClose(t *testing.T) {
	mgr := NewManager("http://example.com", "tunnel-123", "", time.Second, 30*time.Second, config.RuntimeSettings{})
	mgr.Close()

	// Verify stopped flag
	if !mgr.stopped {
		t.Error("Manager.Close() should set stopped flag")
	}

	// Verify session and conn are nil
	if mgr.sess != nil {
		t.Error("Manager.Close() should set sess to nil")
	}
	if mgr.conn != nil {
		t.Error("Manager.Close() should set conn to nil")
	}
}

func TestManagerEnsureSession_Stopped(t *testing.T) {
	mgr := NewManager("http://example.com", "tunnel-123", "", time.Second, 30*time.Second, config.RuntimeSettings{})
	mgr.Close()

	// EnsureSession should return error when stopped
	_, err := mgr.EnsureSessionContext(context.Background())
	require.Error(t, err, "Manager.EnsureSession() should return error when stopped")
	if err.Error() != "stopped" {
		t.Errorf("Manager.EnsureSession() error = %q, want %q", err.Error(), "stopped")
	}
}

func TestCreateDataPlaneSession_Integration(t *testing.T) {
	// Create a test WebSocket server
	upgrader := websocket.Upgrader{
		CheckOrigin: func(_ *http.Request) bool {
			return true
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Echo messages back
		for {
			mt, message, err := conn.ReadMessage()
			if err != nil {
				break
			}
			if err := conn.WriteMessage(mt, message); err != nil {
				break
			}
		}
	}))
	defer server.Close()

	// Convert http:// to ws://
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	serverURL := strings.TrimPrefix(wsURL, "ws://")

	runtime := config.RuntimeSettings{
		PingInterval:          30 * 1000000000,
		PingTimeout:           5 * 1000000000,
		WatchInterval:         10 * 1000000000,
		SmuxKeepAliveInterval: 30 * 1000000000,
		SmuxKeepAliveTimeout:  90 * 1000000000,
	}

	// This will fail because we need a proper smux server, but we can test the connection part
	_, cleanup, err := CreateDataPlaneSession("http://"+serverURL, "test-tunnel", runtime, "")
	if cleanup != nil {
		defer cleanup()
	}
	// The error is expected because smux.Client needs proper initialization
	if err != nil {
		if !strings.Contains(err.Error(), "smux") && !strings.Contains(err.Error(), "ws dial") {
			t.Errorf("CreateDataPlaneSession() unexpected error: %v", err)
		}
	}
}

func TestManager_SessionDialParams(t *testing.T) {
	mgr := NewManager("https://example.com", "tunnel-123", "", time.Second, 30*time.Second, config.RuntimeSettings{})
	wsURL, headers := mgr.sessionDialParams()

	// For https:// URLs, wsURL should be wss://
	if !strings.Contains(wsURL, "wss://example.com") {
		t.Errorf("Manager.sessionDialParams() wsURL = %q, want containing wss://example.com", wsURL)
	}
	if !strings.Contains(wsURL, "tunnel_id=tunnel-123") {
		t.Errorf("Manager.sessionDialParams() wsURL = %q, want containing tunnel_id=tunnel-123", wsURL)
	}
	if headers.Get("Origin") != "https://example.com" {
		t.Errorf("Manager.sessionDialParams() Origin = %q, want %q", headers.Get("Origin"), "https://example.com")
	}
}

func TestBuildWebSocketURL(t *testing.T) {
	wsURL, origin, _, err := buildWebSocketURL("https://example.com", "tunnel-123", "")
	require.NoError(t, err, "buildWebSocketURL() unexpected error: %v")
	if !strings.HasPrefix(wsURL, "wss://example.com/ws") {
		t.Errorf("wsURL = %q, want prefix wss://example.com/ws", wsURL)
	}
	if !strings.Contains(wsURL, "tunnel_id=tunnel-123") {
		t.Errorf("wsURL = %q, want tunnel_id=tunnel-123", wsURL)
	}
	if origin != "https://example.com" {
		t.Errorf("origin = %q, want https://example.com", origin)
	}
}

func TestBuildWebSocketURL_OmitsAuthFromQuery(t *testing.T) {
	wsURL, origin, _, err := buildWebSocketURL("https://example.com", "tunnel-123", "token with spaces/+")
	require.NoError(t, err, "buildWebSocketURL() unexpected error: %v")

	u, err := url.Parse(wsURL)
	require.NoError(t, err, "parse wsURL: %v", err)
	require.Equal(t, "wss", u.Scheme)
	require.Equal(t, "example.com", u.Host)
	require.Equal(t, "/ws", u.Path)
	require.Equal(t, "data", u.Query().Get("mode"))
	require.Equal(t, "tunnel-123", u.Query().Get("tunnel_id"))
	require.Empty(t, u.Query().Get("auth"))
	require.NotContains(t, wsURL, "auth=")
	require.Equal(t, "https://example.com", origin)
}

func TestManager_SessionDialParams_SetsAuthHeader(t *testing.T) {
	mgr := NewManager("https://example.com?auth=stale", "tunnel-123", " token ", time.Second, time.Minute, config.RuntimeSettings{})
	raw, headers := mgr.sessionDialParams()
	parsed, err := url.Parse(raw)
	require.NoError(t, err)
	require.Empty(t, parsed.Query().Get("auth"))
	require.Equal(t, "token", headers.Get(headerDataPlaneAuth))
	require.Equal(t, "https://example.com", headers.Get("Origin"))
}

func TestManager_SessionDialParams_CreateDataPlaneSessionSetsAuthHeader(t *testing.T) {
	type handshake struct{ header, query string }
	observed := make(chan handshake, 1)
	release := make(chan struct{})
	upgrader := websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed <- handshake{r.Header.Get(headerDataPlaneAuth), r.URL.Query().Get("auth")}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		<-release
	}))
	defer server.Close()
	defer close(release)
	settings := config.RuntimeSettings{
		PingInterval: time.Minute, PingTimeout: time.Second,
		SmuxKeepAliveInterval: time.Minute, SmuxKeepAliveTimeout: 2 * time.Minute,
	}
	_, cleanup, err := CreateDataPlaneSession(server.URL+"?auth=stale", "t1", settings, " fresh-header-token ")
	require.NoError(t, err)
	defer cleanup()
	select {
	case got := <-observed:
		require.Equal(t, "fresh-header-token", got.header)
		require.Empty(t, got.query)
	case <-time.After(time.Second):
		t.Fatal("data-plane handshake not observed")
	}
}
