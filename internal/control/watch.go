// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package control

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	protocolv1 "github.com/fortunnels/client/shared/protocol/v1"
	"github.com/gorilla/websocket"

	"github.com/fortunnels/client/internal/config"
)

// logDebug logs at DEBUG level when LOG_LEVEL env contains "debug" or "DEBUG".
func logDebug(format string, args ...any) {
	lvl := strings.ToLower(strings.TrimSpace(os.Getenv("LOG_LEVEL")))
	if strings.Contains(lvl, "debug") {
		log.Printf("[DEBUG] "+format, args...)
	}
}

// authMode describes how the lifecycle poller authenticates to the server.
type authMode string

const (
	authModeSessionCookie authMode = "session-cookie"
	authModeBearer        authMode = "bearer"
	authModeUnauth        authMode = "unauthenticated"
)

type Watcher struct {
	out Output
}

func NewWatcher(out Output) *Watcher {
	if out == nil {
		out = StdOutput{}
	}
	return &Watcher{out: out}
}

func detectAuthMode(client *http.Client, bearer string) authMode {
	if strings.TrimSpace(bearer) != "" {
		return authModeBearer
	}
	if client != nil && client.Jar != nil {
		// Cookie jar indicates session auth was used (login-local)
		return authModeSessionCookie
	}
	return authModeUnauth
}

// ConnectWebSocket connects a control-plane WebSocket and manages keepalive/watchers.
func ConnectWebSocket(serverURL, tunnelID string, runtime config.RuntimeSettings) {
	NewWatcher(nil).ConnectWebSocketWithAuth(nil, serverURL, tunnelID, "", runtime)
}

// ConnectWebSocketWithAuth connects a control-plane WebSocket with optional bearer token
// or session-cookie client for fallback tunnel polling. httpClient may be nil; when
// provided with a cookie jar (session auth), fallback HTTP polls use it for auth.
func ConnectWebSocketWithAuth(httpClient *http.Client, serverURL, tunnelID, bearer string, runtime config.RuntimeSettings) {
	NewWatcher(nil).ConnectWebSocketWithAuth(httpClient, serverURL, tunnelID, bearer, runtime)
}

// ConnectWebSocketWithAuth connects a control-plane WebSocket with optional bearer token
// or session-cookie client for fallback tunnel polling. httpClient may be nil; when
// provided with a cookie jar (session auth), fallback HTTP polls use it for auth.
func (w *Watcher) ConnectWebSocketWithAuth(httpClient *http.Client, serverURL, tunnelID, bearer string, runtime config.RuntimeSettings) {
	wsURL := "ws" + serverURL[4:] + "/ws?watch=" + tunnelID

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		log.Printf("Failed to connect to WebSocket: %v", err)
		return
	}
	defer conn.Close()

	w.out.Printf("✅ WebSocket connected\n")

	ticker := time.NewTicker(runtime.PingInterval)
	defer ticker.Stop()

	done := make(chan struct{})
	var doneOnce sync.Once

	ackCh := make(chan struct{}, 1)
	intervalCh := make(chan time.Duration, 1)

	w.warnOnMissingAck(ackCh)
	w.startFallbackTunnelWatcherWithAuth(httpClient, serverURL, tunnelID, bearer, time.Second, intervalCh, done, &doneOnce)
	w.startControlMessageReader(conn, ackCh, intervalCh, done, &doneOnce, runtime.WatchInterval)

	runPingLoop(conn, ticker, runtime.PingTimeout, done, &doneOnce)
}

func runPingLoop(
	conn *websocket.Conn,
	ticker *time.Ticker,
	pingTimeout time.Duration,
	done chan struct{},
	doneOnce *sync.Once,
) {
	for {
		select {
		case <-ticker.C:
			deadline := time.Now().Add(pingTimeout)
			if err := conn.WriteControl(websocket.PingMessage, nil, deadline); err != nil {
				log.Printf("WebSocket ping loop ending: %v", err)
				doneOnce.Do(func() { close(done) })
				return
			}
		case <-done:
			return
		}
	}
}

func (w *Watcher) warnOnMissingAck(ackCh <-chan struct{}) {
	go func() {
		select {
		case <-ackCh:
			return
		case <-time.After(5 * time.Second):
			logDebug("falling back from WS subscription ACK path to HTTP poll path")
			w.out.Println("⚠️ No 'subscribed' ACK received from server; relying on fallback monitoring")
		}
	}()
}

// startFallbackTunnelWatcherWithAuth runs a background goroutine that polls the server
// for tunnel terminal state. httpClient may be nil; when provided with a cookie jar
// (session auth), it is used for authenticated requests. bearer is used when non-empty.
func (w *Watcher) startFallbackTunnelWatcherWithAuth(
	httpClient *http.Client,
	serverURL, tunnelID, bearer string,
	initialInterval time.Duration,
	intervalCh <-chan time.Duration,
	done chan struct{},
	doneOnce *sync.Once,
) {
	client := ensurePollClient(httpClient)
	mode := detectAuthMode(client, bearer)
	logDebug("fallback tunnel watcher: auth mode=%s tunnelID=%s", mode, tunnelID)

	go func() {
		ticker := time.NewTicker(initialInterval)
		defer ticker.Stop()
		var consecutiveFailures int
		lastStatus := statusActive
		for {
			select {
			case <-ticker.C:
				terminal, status, statusCode := checkTunnelTerminalWithStatusImpl(client, serverURL, tunnelID, bearer)
				if terminal {
					w.out.Println(MsgTunnelRemovedExiting)
					doneOnce.Do(func() { close(done) })
					return
				}
				if status != "" && status != lastStatus {
					w.printTunnelStatusChange(status)
					lastStatus = status
				}
				if statusCode >= 400 {
					consecutiveFailures++
					if statusCode == 403 {
						log.Printf("[WARN] auth failure from fallback GET tunnelID=%s status=%d", tunnelID, statusCode)
					}
					if consecutiveFailures >= 2 {
						log.Printf("[WARN] repeated poll failures tunnelID=%s status=%d (attempt %d)",
							tunnelID, statusCode, consecutiveFailures)
					}
				} else {
					consecutiveFailures = 0
				}
			case d := <-intervalCh:
				if d > 0 {
					ticker.Reset(d)
				}
			case <-done:
				return
			}
		}
	}()
}

// StatusExpired is the wire value for expired tunnel status.
const StatusExpired = protocolv1.StatusExpired

// Lifecycle contract: HTTP 401 from GET /api/tunnels?id=<id> means the tunnel
// was removed for this client session/token (e.g. revoked access, session expired).
// The client must treat this as terminal and exit cleanly. Technical details
// (tunnelID, auth mode, status) belong in DEBUG; user-facing output uses
// MsgTunnelRemovedExiting.
const MsgTunnelRemovedExiting = "Tunnel was removed. Exiting."

const (
	statusActive    = protocolv1.StatusActive
	statusNotActive = protocolv1.StatusNotActive
	statusPaused    = protocolv1.StatusPaused
)

// ensurePollClient returns a client suitable for polling. If httpClient is nil or
// has no timeout, a default client with 2s timeout is used. Preserves cookie jar
// when present for session auth.
func ensurePollClient(httpClient *http.Client) *http.Client {
	if httpClient == nil {
		return &http.Client{Timeout: 2 * time.Second}
	}
	if httpClient.Timeout <= 0 {
		c := *httpClient
		c.Timeout = 2 * time.Second
		return &c
	}
	return httpClient
}

// RunFallbackLifecyclePoller polls the server and calls onTerminal when the tunnel
// is deleted or expired. Use for HTTP/TCP/UDP modes that lack in-band lifecycle.
// httpClient may be nil; when provided with a cookie jar (session auth), it is used
// for authenticated requests. bearer is used when non-empty.
func RunFallbackLifecyclePoller(httpClient *http.Client, serverURL, tunnelID, bearer string, onTerminal func(), interval time.Duration) {
	NewWatcher(nil).RunFallbackLifecyclePoller(httpClient, serverURL, tunnelID, bearer, onTerminal, interval)
}

func (w *Watcher) RunFallbackLifecyclePoller(httpClient *http.Client, serverURL, tunnelID, bearer string, onTerminal func(), interval time.Duration) {
	client := ensurePollClient(httpClient)
	mode := detectAuthMode(client, bearer)
	logDebug("fallback lifecycle poller: auth mode=%s tunnelID=%s", mode, tunnelID)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var consecutiveFailures int
	lastStatus := statusActive
	for {
		<-ticker.C
		terminal, status, statusCode := checkTunnelTerminalWithStatusImpl(client, serverURL, tunnelID, bearer)
		if terminal {
			w.out.Println(MsgTunnelRemovedExiting)
			onTerminal()
			return
		}
		if status != "" && status != lastStatus {
			w.printTunnelStatusChange(status)
			lastStatus = status
		}
		if statusCode >= 400 {
			consecutiveFailures++
			if statusCode == 403 {
				log.Printf("[WARN] auth failure from fallback GET tunnelID=%s status=%d", tunnelID, statusCode)
			}
			if consecutiveFailures >= 2 {
				log.Printf("[WARN] repeated poll failures tunnelID=%s status=%d (attempt %d)",
					tunnelID, statusCode, consecutiveFailures)
			}
		} else {
			consecutiveFailures = 0
		}
	}
}

// checkTunnelTerminalWithStatus returns (terminal, statusCode). statusCode is the
// HTTP response status when non-terminal; 0 when request failed before response.
//
//nolint:unparam // tunnelID is required for API; unparam flags test-only call sites
func checkTunnelTerminalWithStatus(client *http.Client, serverURL, tunnelID, bearer string) (terminal bool, statusCode int) {
	terminal, _, statusCode = checkTunnelTerminalWithStatusImpl(client, serverURL, tunnelID, bearer)
	return terminal, statusCode
}

func checkTunnelTerminal(client *http.Client, serverURL, tunnelID, bearer string) bool {
	terminal, _, _ := checkTunnelTerminalWithStatusImpl(client, serverURL, tunnelID, bearer)
	return terminal
}

func checkTunnelTerminalWithStatusImpl(client *http.Client, serverURL, tunnelID, bearer string) (terminal bool, status string, statusCode int) {
	timeout := client.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", serverURL+"/api/tunnels?id="+tunnelID, http.NoBody)
	if err != nil {
		return false, "", 0
	}
	if strings.TrimSpace(bearer) != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, "", 0
	}
	defer resp.Body.Close()

	// 401/403 from GET /api/tunnels?id=<id> means tunnel removed or access revoked for this session/token.
	// Treat as terminal immediately; no failure counters or WARN spam.
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		logDebug("%d from GET /api/tunnels → terminal (tunnel removed or access revoked)", resp.StatusCode)
		return true, "", resp.StatusCode
	}

	var payload protocolv1.TunnelListResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return false, "", resp.StatusCode
	}
	status = tunnelStatusFromPayload(payload)

	if !payload.Exists {
		return true, status, resp.StatusCode
	}
	if status == StatusExpired {
		return true, status, resp.StatusCode
	}
	return false, status, resp.StatusCode
}

func tunnelStatusFromPayload(payload any) string {
	switch v := payload.(type) {
	case protocolv1.TunnelListResponse:
		if v.Status != "" {
			return v.Status
		}
		if len(v.Tunnels) > 0 && v.Tunnels[0].Status != "" {
			return v.Tunnels[0].Status
		}
	case map[string]interface{}:
		if status, ok := v["status"].(string); ok && status != "" {
			return status
		}
		if tunnels, ok := v["tunnels"].([]interface{}); ok && len(tunnels) > 0 {
			if tunnel, ok := tunnels[0].(map[string]interface{}); ok {
				if status, ok := tunnel["status"].(string); ok {
					return status
				}
			}
		}
	}
	return ""
}

func (w *Watcher) printTunnelStatusChange(status string) {
	switch status {
	case StatusExpired:
		return
	case statusActive:
		w.out.Printf("✅ Tunnel status changed to active on server\n")
	case statusPaused:
		w.out.Printf("⏸️ Tunnel status changed to paused on server\n")
	case statusNotActive:
		w.out.Printf("⚪ Tunnel status changed to not active on server\n")
	default:
		w.out.Printf("📨 Tunnel status changed on server: %s\n", status)
	}
}

func checkTunnelDeleted(client *http.Client, serverURL, tunnelID string) bool {
	return checkTunnelTerminal(client, serverURL, tunnelID, "")
}

func (w *Watcher) startControlMessageReader(
	conn *websocket.Conn,
	ackCh chan<- struct{},
	intervalCh chan<- time.Duration,
	done chan struct{},
	doneOnce *sync.Once,
	defaultWatchInterval time.Duration,
) {
	go func() {
		lastStatus := statusActive
		for {
			var msg protocolv1.Envelope
			if err := conn.ReadJSON(&msg); err != nil {
				logWebSocketReadError(err)
				doneOnce.Do(func() { close(done) })
				return
			}
			if w.handleControlMessage(msg, ackCh, intervalCh, done, doneOnce, defaultWatchInterval, &lastStatus) {
				return
			}
		}
	}()
}

func logWebSocketReadError(err error) {
	var ce *websocket.CloseError
	switch {
	case errors.As(err, &ce):
		switch ce.Code {
		case websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseAbnormalClosure:
			log.Printf("WebSocket closed by server (code %d). Exiting cleanly.", ce.Code)
		default:
			log.Printf("WebSocket closed (code %d): %v. Exiting.", ce.Code, err)
		}
	case errors.Is(err, io.EOF), strings.Contains(err.Error(), "unexpected EOF"):
		log.Printf("WebSocket connection ended (EOF). Exiting cleanly.")
	default:
		log.Printf("WebSocket ended: %v", err)
	}
}

func (w *Watcher) handleControlMessage(
	msg protocolv1.Envelope,
	ackCh chan<- struct{},
	intervalCh chan<- time.Duration,
	done chan struct{},
	doneOnce *sync.Once,
	defaultWatchInterval time.Duration,
	lastStatus *string,
) bool {
	switch msg.Type {
	case protocolv1.MessageTypePong:
		w.out.Printf("💓 Ping received at %s\n", time.Now().Format("15:04:05"))
	case protocolv1.EventTunnelClosed:
		reason := extractTunnelCloseReason(msg)
		logDebug("tunnel_closed reason=%s", reason)
		w.out.Println(MsgTunnelRemovedExiting)
		doneOnce.Do(func() { close(done) })
		return true
	case protocolv1.EventTunnelUpdated:
		var payload protocolv1.LifecycleEventPayload
		if err := msg.DecodePayload(&payload); err == nil {
			if payload.Status == StatusExpired {
				w.out.Println(MsgTunnelRemovedExiting)
				doneOnce.Do(func() { close(done) })
				return true
			}
			if payload.Status != "" && (lastStatus == nil || *lastStatus != payload.Status) {
				w.printTunnelStatusChange(payload.Status)
				if lastStatus != nil {
					*lastStatus = payload.Status
				}
			}
		}
	case protocolv1.MessageTypeSubscribed:
		notifyAckReceived(ackCh)
		updateFallbackInterval(intervalCh, defaultWatchInterval)
		w.out.Printf("📨 Message: %s\n", msg.Type)
	case protocolv1.MessageTypeError:
		var payload protocolv1.ErrorPayload
		if err := msg.DecodePayload(&payload); err == nil && payload.Message != "" {
			w.out.Printf("❌ Error: %s\n", payload.Message)
		}
	default:
		w.out.Printf("📨 Message: %s\n", msg.Type)
	}
	return false
}

func handleControlMessage(
	msg map[string]interface{},
	ackCh chan<- struct{},
	intervalCh chan<- time.Duration,
	done chan struct{},
	doneOnce *sync.Once,
	defaultWatchInterval time.Duration,
	lastStatus *string,
) bool {
	return NewWatcher(nil).handleControlMessage(
		envelopeFromMap(msg),
		ackCh,
		intervalCh,
		done,
		doneOnce,
		defaultWatchInterval,
		lastStatus,
	)
}

func extractTunnelCloseReason(msg any) string {
	var payload protocolv1.LifecycleEventPayload
	switch v := msg.(type) {
	case protocolv1.Envelope:
		if err := v.DecodePayload(&payload); err == nil && payload.Reason != "" {
			return payload.Reason
		}
	case map[string]interface{}:
		if raw, ok := v["payload"].(map[string]interface{}); ok {
			if reason, ok := raw["reason"].(string); ok && reason != "" {
				return reason
			}
		}
	}
	return protocolv1.ReasonUnknown
}

func extractPayload(msg map[string]interface{}) map[string]interface{} {
	payload, _ := msg["payload"].(map[string]interface{})
	return payload
}

func envelopeFromMap(msg map[string]interface{}) protocolv1.Envelope {
	envelope := protocolv1.Envelope{}
	if msgType, ok := msg["type"].(string); ok {
		envelope.Type = msgType
	}
	if payload, ok := msg["payload"]; ok {
		if data, err := json.Marshal(payload); err == nil {
			envelope.Payload = data
		}
	}
	return envelope
}

func notifyAckReceived(ackCh chan<- struct{}) {
	select {
	case ackCh <- struct{}{}:
	default:
	}
}

func updateFallbackInterval(intervalCh chan<- time.Duration, d time.Duration) {
	if d <= 0 {
		return
	}
	select {
	case intervalCh <- d:
	default:
	}
}
