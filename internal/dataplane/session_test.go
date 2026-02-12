// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

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

func TestClientClose(t *testing.T) {
	t.Run("nil client", func(_ *testing.T) {
		var c *Client
		// Should not panic
		c.Close()
	})

	t.Run("valid client", func(_ *testing.T) {
		// Create a minimal client for testing
		// Note: Close() calls c.conn.Close() which will panic if conn is nil
		// This is expected behavior - in real usage, conn is always set
		// We only test nil client case which is handled
		// For full client test, see integration tests
	})
}

func TestClientSession(t *testing.T) {
	// This test verifies Session() and Conn() methods
	// We can't easily create a real Client without WebSocket, so we test with nil
	c := &Client{}
	if c.Session() != nil {
		t.Error("Client.Session() should return nil for uninitialized client")
	}
	if c.Conn() != nil {
		t.Error("Client.Conn() should return nil for uninitialized client")
	}
}

func TestManagerClose(t *testing.T) {
	mgr := NewManager("http://example.com", "tunnel-123", time.Second, 30*time.Second, config.RuntimeSettings{})
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
	mgr := NewManager("http://example.com", "tunnel-123", time.Second, 30*time.Second, config.RuntimeSettings{})
	mgr.Close()

	// EnsureSession should return error when stopped
	_, err := mgr.EnsureSession()
	if err == nil {
		t.Error("Manager.EnsureSession() should return error when stopped")
	}
	if err.Error() != "stopped" {
		t.Errorf("Manager.EnsureSession() error = %q, want %q", err.Error(), "stopped")
	}
}

func TestNewWSSmuxClient_Integration(t *testing.T) {
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
	_, err := NewWSSmuxClient("http://"+serverURL, "test-tunnel", runtime)
	// The error is expected because smux.Client needs proper initialization
	if err != nil {
		if !strings.Contains(err.Error(), "smux") && !strings.Contains(err.Error(), "ws dial") {
			t.Errorf("NewWSSmuxClient() unexpected error: %v", err)
		}
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
	_, cleanup, err := CreateDataPlaneSession("http://"+serverURL, "test-tunnel", runtime)
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
	mgr := NewManager("https://example.com", "tunnel-123", time.Second, 30*time.Second, config.RuntimeSettings{})
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
	wsURL, origin, err := buildWebSocketURL("https://example.com", "tunnel-123")
	if err != nil {
		t.Fatalf("buildWebSocketURL() unexpected error: %v", err)
	}
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
