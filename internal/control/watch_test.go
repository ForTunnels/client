// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"

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

func TestRunFallbackLifecyclePoller_401TriggersOnTerminalAndFriendlyMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	terminalCh := make(chan struct{}, 1)
	var onTerminalCalled int
	onTerminal := func() {
		onTerminalCalled++
		close(terminalCh)
	}

	output := captureStdout(t, func() {
		go RunFallbackLifecyclePoller(client, server.URL, "t1", "", onTerminal, 20*time.Millisecond)
		select {
		case <-terminalCh:
			// Good
		case <-time.After(2 * time.Second):
			t.Fatal("onTerminal was not called within 2s")
		}
	})

	assert.Equal(t, 1, onTerminalCalled, "onTerminal must be called exactly once")
	assert.Contains(t, output, MsgTunnelRemovedExiting, "friendly removal message must be printed")
}

func TestRunFallbackLifecyclePoller_401NoWarnSpam(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	terminalCh := make(chan struct{}, 1)
	onTerminal := func() { close(terminalCh) }

	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	defer log.SetOutput(os.Stderr)

	go RunFallbackLifecyclePoller(client, server.URL, "t1", "", onTerminal, 20*time.Millisecond)
	select {
	case <-terminalCh:
		// Good
	case <-time.After(2 * time.Second):
		t.Fatal("onTerminal was not called within 2s")
	}

	logOut := logBuf.String()
	assert.NotContains(t, logOut, "[WARN] auth failure", "401 removal path must not emit WARN spam")
	assert.NotContains(t, logOut, "[WARN] repeated poll failures", "401 removal path must not emit WARN spam")
}

func TestRunFallbackLifecyclePoller_403TerminalAndNoWarnSpam(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	terminalCh := make(chan struct{}, 1)
	var onTerminalCalled int
	onTerminal := func() {
		onTerminalCalled++
		close(terminalCh)
	}

	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	defer log.SetOutput(os.Stderr)

	output := captureStdout(t, func() {
		go RunFallbackLifecyclePoller(client, server.URL, "t1", "", onTerminal, 20*time.Millisecond)
		select {
		case <-terminalCh:
			// Good
		case <-time.After(2 * time.Second):
			t.Fatal("onTerminal was not called within 2s")
		}
	})

	assert.Equal(t, 1, onTerminalCalled, "onTerminal must be called exactly once for 403")
	assert.Contains(t, output, MsgTunnelRemovedExiting, "friendly removal message must be printed for 403")
	logOut := logBuf.String()
	assert.NotContains(t, logOut, "[WARN] auth failure", "403 removal path must not emit WARN spam")
	assert.NotContains(t, logOut, "[WARN] repeated poll failures", "403 removal path must not emit WARN spam")
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

func TestDetectAuthMode(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	tests := []struct {
		name   string
		client *http.Client
		bearer string
		want   authMode
	}{
		{"bearer", &http.Client{}, "token", authModeBearer},
		{"session-cookie", &http.Client{Jar: jar}, "", authModeSessionCookie},
		{"unauthenticated", &http.Client{}, "", authModeUnauth},
		{"bearer overrides jar", &http.Client{Jar: jar}, "t", authModeBearer},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectAuthMode(tt.client, tt.bearer)
			assert.Equal(t, tt.want, got)
		})
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
			assert.Equal(t, tt.expected, result, "extractTunnelCloseReason() = %v, want %v")
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
		payload  map[string]interface{}
		expected string
	}{
		{
			name:     "uses top-level status",
			payload:  map[string]interface{}{"status": statusActive},
			expected: statusActive,
		},
		{
			name: "uses first tunnel status when top-level missing",
			payload: map[string]interface{}{
				"tunnels": []interface{}{map[string]interface{}{"status": statusPaused}},
			},
			expected: statusPaused,
		},
		{
			name:     "top-level status takes precedence",
			expected: statusActive,
			payload: map[string]interface{}{
				"status":  statusActive,
				"tunnels": []interface{}{map[string]interface{}{"status": statusPaused}},
			},
		},
		{
			name:     "empty when no status",
			payload:  map[string]interface{}{"exists": true},
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
