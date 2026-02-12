// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package control

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fortunnels/client/internal/config"
)

func TestCheckTunnelDeleted(t *testing.T) {
	tests := []struct {
		name     string
		response map[string]interface{}
		expected bool
	}{
		{
			name:     "tunnel exists",
			response: map[string]interface{}{"exists": true},
			expected: false,
		},
		{
			name:     "tunnel deleted",
			response: map[string]interface{}{"exists": false},
			expected: true,
		},
		{
			name:     "missing exists field",
			response: map[string]interface{}{"id": "tunnel-123"},
			expected: false,
		},
		{
			name:     "invalid exists type",
			response: map[string]interface{}{"exists": "yes"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(tt.response)
			}))
			defer server.Close()

			client := &http.Client{Timeout: 2 * time.Second}
			result := checkTunnelDeleted(client, server.URL, "tunnel-123")
			if result != tt.expected {
				t.Errorf("checkTunnelDeleted() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestCheckTunnelDeleted_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	result := checkTunnelDeleted(client, server.URL, "tunnel-123")
	if result {
		t.Error("checkTunnelDeleted() with HTTP error should return false")
	}
}

func TestCheckTunnelDeleted_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("invalid json"))
	}))
	defer server.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	result := checkTunnelDeleted(client, server.URL, "tunnel-123")
	if result {
		t.Error("checkTunnelDeleted() with invalid JSON should return false")
	}
}

func TestExtractPayload(t *testing.T) {
	tests := []struct {
		name     string
		msg      map[string]interface{}
		expected map[string]interface{}
	}{
		{
			name:     "valid payload",
			msg:      map[string]interface{}{"payload": map[string]interface{}{"key": "value"}},
			expected: map[string]interface{}{"key": "value"},
		},
		{
			name:     "no payload",
			msg:      map[string]interface{}{"type": "pong"},
			expected: nil,
		},
		{
			name:     "invalid payload type",
			msg:      map[string]interface{}{"payload": "not a map"},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractPayload(tt.msg)
			if result == nil && tt.expected != nil {
				t.Error("extractPayload() returned nil, expected non-nil")
			}
			if result != nil && tt.expected == nil {
				t.Error("extractPayload() returned non-nil, expected nil")
			}
		})
	}
}

func TestExtractTunnelCloseReason(t *testing.T) {
	tests := []struct {
		name     string
		msg      map[string]interface{}
		expected string
	}{
		{
			name:     "valid reason",
			msg:      map[string]interface{}{"payload": map[string]interface{}{"reason": "user_request"}},
			expected: "user_request",
		},
		{
			name:     "no payload",
			msg:      map[string]interface{}{"type": "tunnel_closed"},
			expected: "unknown",
		},
		{
			name:     "no reason field",
			msg:      map[string]interface{}{"payload": map[string]interface{}{"other": "value"}},
			expected: "unknown",
		},
		{
			name:     "empty reason",
			msg:      map[string]interface{}{"payload": map[string]interface{}{"reason": ""}},
			expected: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractTunnelCloseReason(tt.msg)
			if result != tt.expected {
				t.Errorf("extractTunnelCloseReason() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestHandleControlMessage(t *testing.T) {
	tests := []struct {
		name         string
		msg          map[string]interface{}
		shouldReturn bool
	}{
		{
			name:         "pong message",
			msg:          map[string]interface{}{"type": "pong"},
			shouldReturn: false,
		},
		{
			name:         "tunnel_closed",
			msg:          map[string]interface{}{"type": "tunnel_closed", "payload": map[string]interface{}{"reason": "deleted"}},
			shouldReturn: true,
		},
		{
			name:         "subscribed",
			msg:          map[string]interface{}{"type": "subscribed"},
			shouldReturn: false,
		},
		{
			name:         "error message",
			msg:          map[string]interface{}{"type": "error", "payload": map[string]interface{}{"message": "test error"}},
			shouldReturn: false,
		},
		{
			name:         "unknown type",
			msg:          map[string]interface{}{"type": "unknown"},
			shouldReturn: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ackCh := make(chan struct{}, 1)
			intervalCh := make(chan time.Duration, 1)
			done := make(chan struct{})
			var doneOnce sync.Once

			result := handleControlMessage(tt.msg, ackCh, intervalCh, done, &doneOnce, 10*time.Second)
			if result != tt.shouldReturn {
				t.Errorf("handleControlMessage() = %v, want %v", result, tt.shouldReturn)
			}
		})
	}
}

func TestNotifyAckReceived(t *testing.T) {
	ackCh := make(chan struct{}, 1)
	notifyAckReceived(ackCh)

	select {
	case <-ackCh:
		// Good, ACK was sent
	case <-time.After(100 * time.Millisecond):
		t.Error("notifyAckReceived() did not send ACK")
	}
}

func TestNotifyAckReceived_FullChannel(t *testing.T) {
	ackCh := make(chan struct{}, 1)
	ackCh <- struct{}{} // Fill channel
	// Should not block
	notifyAckReceived(ackCh)
}

func TestUpdateFallbackInterval(t *testing.T) {
	intervalCh := make(chan time.Duration, 1)
	updateFallbackInterval(intervalCh, 5*time.Second)

	select {
	case d := <-intervalCh:
		if d != 5*time.Second {
			t.Errorf("updateFallbackInterval() = %v, want %v", d, 5*time.Second)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("updateFallbackInterval() did not send interval")
	}
}

func TestUpdateFallbackInterval_Zero(t *testing.T) {
	intervalCh := make(chan time.Duration, 1)
	updateFallbackInterval(intervalCh, 0)

	select {
	case <-intervalCh:
		t.Error("updateFallbackInterval() should not send zero interval")
	case <-time.After(100 * time.Millisecond):
		// Good, nothing sent
	}
}

func TestLogWebSocketReadError(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{
			name: "normal closure",
			err:  &websocket.CloseError{Code: websocket.CloseNormalClosure},
		},
		{
			name: "going away",
			err:  &websocket.CloseError{Code: websocket.CloseGoingAway},
		},
		{
			name: "abnormal closure",
			err:  &websocket.CloseError{Code: websocket.CloseAbnormalClosure},
		},
		{
			name: "other close code",
			err:  &websocket.CloseError{Code: websocket.CloseInternalServerErr},
		},
		{
			name: "EOF",
			err:  io.EOF,
		},
		{
			name: "unexpected EOF",
			err:  io.ErrUnexpectedEOF,
		},
		{
			name: "other error",
			err:  errors.New("some error"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Should not panic
			logWebSocketReadError(tt.err)
		})
	}
}

func TestConnectWebSocket_Integration(t *testing.T) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Send subscribed message
		msg := map[string]interface{}{"type": "subscribed"}
		conn.WriteJSON(msg)

		// Send pong
		msg = map[string]interface{}{"type": "pong"}
		conn.WriteJSON(msg)

		// Keep connection alive for a bit
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	// Convert http:// to ws://
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	serverURL := strings.TrimPrefix(wsURL, "ws://")

	runtime := config.RuntimeSettings{
		PingInterval:  1 * 1000000000,  // 1 second
		PingTimeout:   500 * 1000000,   // 500ms
		WatchInterval: 10 * 1000000000, // 10 seconds
	}

	// This function runs indefinitely, so we'll test it with a timeout
	done := make(chan struct{})
	go func() {
		ConnectWebSocket("http://"+serverURL, "test-tunnel", runtime)
		close(done)
	}()

	// Wait a bit to see if it connects
	select {
	case <-done:
		// Function returned (expected)
	case <-time.After(2 * time.Second):
		// Still running (expected for this function)
	}
}
