//go:build integration

// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package control

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"

	"github.com/fortunnels/client/internal/config"
	protocolv1 "github.com/fortunnels/client/shared/protocol/v1"
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
			expected: true,
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
			assert.Equal(t, tt.expected, result, "checkTunnelDeleted() = %v, want %v")
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

func TestCheckTunnelTerminalWithStatus_BearerAuth(t *testing.T) {
	var capturedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"exists": true})
	}))
	defer server.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	terminal, status := checkTunnelTerminalWithStatus(client, server.URL, "t1", "my-bearer-token")
	assert.False(t, terminal)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, "Bearer my-bearer-token", capturedAuth)
}

func TestCheckTunnelTerminal_NonOKJSONBodyIsNotTerminal(t *testing.T) {
	for _, statusCode := range []int{http.StatusServiceUnavailable, http.StatusTooManyRequests, http.StatusBadGateway} {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(statusCode)
				_, _ = io.WriteString(w, `{"error":"temporarily unavailable"}`)
			}))
			defer server.Close()

			terminal, status, code, terminalErr := checkTunnelTerminalDetailedContext(
				context.Background(), server.Client(), server.URL, "t1", "",
			)
			assert.False(t, terminal)
			assert.Empty(t, status)
			assert.Equal(t, statusCode, code)
			assert.NoError(t, terminalErr)
		})
	}
}

func TestCheckTunnelTerminalWithStatus_CookieJarAuth(t *testing.T) {
	jar, err := cookiejar.New(nil)
	assert.NoError(t, err)
	client := &http.Client{Timeout: 2 * time.Second, Jar: jar}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Cookie jar auth: no bearer, but cookies from same domain are sent
		_ = r.Cookies()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"exists": true})
	}))
	defer server.Close()

	terminal, status := checkTunnelTerminalWithStatus(client, server.URL, "t1", "")
	assert.False(t, terminal)
	assert.Equal(t, http.StatusOK, status)
}

func TestCheckTunnelTerminalWithStatus_UnauthorizedTerminal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	terminal, status := checkTunnelTerminalWithStatus(client, server.URL, "t1", "")
	assert.True(t, terminal, "401 from GET /api/tunnels means tunnel removed for this session; must be terminal")
	assert.Equal(t, http.StatusUnauthorized, status)
}

func TestCheckTunnelTerminalWithStatus_ForbiddenTerminal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	terminal, status := checkTunnelTerminalWithStatus(client, server.URL, "t1", "")
	assert.True(t, terminal, "403 from GET /api/tunnels means access revoked; must be terminal")
	assert.Equal(t, http.StatusForbidden, status)
}

func TestCheckTunnelTerminalWithStatus_ExpiredDetection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"status": StatusExpired})
	}))
	defer server.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	terminal, status := checkTunnelTerminalWithStatus(client, server.URL, "t1", "")
	assert.True(t, terminal)
	assert.Equal(t, http.StatusOK, status)
}

func TestCheckTunnelTerminalWithStatus_DeletedDetection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"exists": false})
	}))
	defer server.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	terminal, status := checkTunnelTerminalWithStatus(client, server.URL, "t1", "")
	assert.True(t, terminal)
	assert.Equal(t, http.StatusOK, status)
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

func TestDecodeTunnelClosePayloadReason(t *testing.T) {
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
			result := decodeTunnelClosePayload(envelopeFromMap(tt.msg))
			assert.Equal(t, tt.expected, result.Reason, "decodeTunnelClosePayload() reason = %v, want %v")
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
			name:         "tunnel updated paused",
			msg:          map[string]interface{}{"type": "tunnel_updated", "payload": map[string]interface{}{"status": "paused"}},
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
			lastStatus := ""
			var doneOnce sync.Once

			result := handleControlMessage(tt.msg, ackCh, intervalCh, done, &doneOnce, 10*time.Second, &lastStatus)
			assert.Equal(t, tt.shouldReturn, result, "handleControlMessage() = %v, want %v")
		})
	}
}

func TestHandleControlMessageRepeatedTunnelUpdatedStatus(t *testing.T) {
	msg := map[string]interface{}{"type": "tunnel_updated", "payload": map[string]interface{}{"status": statusPaused}}
	ackCh := make(chan struct{}, 1)
	intervalCh := make(chan time.Duration, 1)
	done := make(chan struct{})
	var doneOnce sync.Once
	lastStatus := ""

	output := captureStdout(t, func() {
		result := handleControlMessage(msg, ackCh, intervalCh, done, &doneOnce, 10*time.Second, &lastStatus)
		assert.False(t, result)
		result = handleControlMessage(msg, ackCh, intervalCh, done, &doneOnce, 10*time.Second, &lastStatus)
		assert.False(t, result)
	})

	select {
	case <-done:
		t.Fatal("done channel should not be closed for non-terminal status update")
	default:
	}
	assert.Equal(t, 1, strings.Count(output, "⏸️ Tunnel status changed to paused on server\n"))
}

func TestHandleControlMessageSuppressesInitialActive(t *testing.T) {
	msg := map[string]interface{}{"type": "tunnel_updated", "payload": map[string]interface{}{"status": statusActive}}
	ackCh := make(chan struct{}, 1)
	intervalCh := make(chan time.Duration, 1)
	done := make(chan struct{})
	var doneOnce sync.Once
	lastStatus := statusActive

	output := captureStdout(t, func() {
		result := handleControlMessage(msg, ackCh, intervalCh, done, &doneOnce, 10*time.Second, &lastStatus)
		assert.False(t, result)
	})

	select {
	case <-done:
		t.Fatal("done channel should not be closed for active status update")
	default:
	}
	assert.Equal(t, 0, strings.Count(output, "✅ Tunnel status changed to active on server"))
}

func TestTunnelStatusFromPayload(t *testing.T) {
	tests := []struct {
		name     string
		payload  any
		expected string
	}{
		{
			name:     "uses typed top-level status",
			payload:  protocolv1.TunnelListResponse{Status: statusActive},
			expected: statusActive,
		},
		{
			name:     "empty when no status",
			payload:  protocolv1.TunnelListResponse{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := tunnelStatusFromPayload(tt.payload)
			assert.Equal(t, tt.expected, actual)
		})
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() failed: %v", err)
	}
	defer func() {
		os.Stdout = origStdout
	}()

	os.Stdout = w
	fn()

	if err := w.Close(); err != nil {
		t.Logf("failed to close stdout pipe writer: %v", err)
	}

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	if err := r.Close(); err != nil {
		t.Logf("failed to close stdout pipe reader: %v", err)
	}
	return buf.String()
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
		assert.Equal(t, 5*time.Second, d, "updateFallbackInterval() = %v, want %v")
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

func TestRunLifecycleWatch_ReconnectsAfterServerClose(t *testing.T) {
	var upgrades atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if upgrades.Add(1) == 1 {
			return
		}
		_ = conn.WriteJSON(map[string]any{"type": "tunnel_closed", "payload": map[string]string{"reason": "user_request"}})
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	terminal := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		RunLifecycleWatch(ctx, server.Client(), server.URL, "t1", "", config.RuntimeSettings{PingInterval: time.Hour, PingTimeout: time.Second}, func(err error) { terminal <- err })
		close(done)
	}()
	select {
	case err := <-terminal:
		assert.EqualError(t, err, MsgTunnelRemovedExiting)
	case <-ctx.Done():
		t.Fatal("terminal event did not arrive after reconnect")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watch did not end after terminal event")
	}
	assert.GreaterOrEqual(t, upgrades.Load(), int32(2))
}

func TestRunLifecycleWatch_StopsOnContextCancel(t *testing.T) {
	for _, holdHandshake := range []bool{false, true} {
		name := "connected"
		if holdHandshake {
			name = "handshake"
		}
		t.Run(name, func(t *testing.T) {
			requested, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if holdHandshake {
					close(requested)
					<-release
				}
				upgrader := websocket.Upgrader{}
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				if !holdHandshake {
					close(requested)
				}
				_, _, _ = conn.ReadMessage()
			}))
			defer server.Close()
			defer unblock()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var terminal atomic.Bool
			done := make(chan struct{})
			go func() {
				RunLifecycleWatch(ctx, server.Client(), server.URL, "t1", "", config.RuntimeSettings{PingInterval: time.Hour, PingTimeout: time.Second}, func(error) { terminal.Store(true) })
				close(done)
			}()
			select {
			case <-requested:
			case <-time.After(time.Second):
				t.Fatal("watch did not start")
			}
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("watch ignored cancellation")
			}
			assert.False(t, terminal.Load())
		})
	}
}
