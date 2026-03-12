// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

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
	ConnectWebSocketWithAuth(nil, serverURL, tunnelID, "", runtime)
}

// ConnectWebSocketWithAuth connects a control-plane WebSocket with optional bearer token
// or session-cookie client for fallback tunnel polling. httpClient may be nil; when
// provided with a cookie jar (session auth), fallback HTTP polls use it for auth.
func ConnectWebSocketWithAuth(httpClient *http.Client, serverURL, tunnelID, bearer string, runtime config.RuntimeSettings) {
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

	fmt.Printf("✅ WebSocket connected\n")

	ticker := time.NewTicker(runtime.PingInterval)
	defer ticker.Stop()

	done := make(chan struct{})
	var doneOnce sync.Once

	ackCh := make(chan struct{}, 1)
	intervalCh := make(chan time.Duration, 1)

	warnOnMissingAck(ackCh)
	startFallbackTunnelWatcherWithAuth(httpClient, serverURL, tunnelID, bearer, time.Second, intervalCh, done, &doneOnce)
	startControlMessageReader(conn, ackCh, intervalCh, done, &doneOnce, runtime.WatchInterval)

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

func warnOnMissingAck(ackCh <-chan struct{}) {
	go func() {
		select {
		case <-ackCh:
			return
		case <-time.After(5 * time.Second):
			logDebug("falling back from WS subscription ACK path to HTTP poll path")
			fmt.Println("⚠️ No 'subscribed' ACK received from server; relying on fallback monitoring")
		}
	}()
}

func startFallbackTunnelWatcher(
	serverURL, tunnelID string,
	initialInterval time.Duration,
	intervalCh <-chan time.Duration,
	done chan struct{},
	doneOnce *sync.Once,
) {
	startFallbackTunnelWatcherWithAuth(nil, serverURL, tunnelID, "", initialInterval, intervalCh, done, doneOnce)
}

// startFallbackTunnelWatcherWithAuth runs a background goroutine that polls the server
// for tunnel terminal state. httpClient may be nil; when provided with a cookie jar
// (session auth), it is used for authenticated requests. bearer is used when non-empty.
func startFallbackTunnelWatcherWithAuth(
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
					fmt.Printf("🔴 Tunnel deleted or expired on server\n")
					doneOnce.Do(func() { close(done) })
					return
				}
				if status != "" && status != lastStatus {
					printTunnelStatusChange(status)
					lastStatus = status
				}
				if statusCode >= 400 {
					consecutiveFailures++
					if statusCode == 401 || statusCode == 403 {
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
const StatusExpired = "expired"

const (
	statusActive    = "active"
	statusNotActive = "not active"
	statusPaused    = "paused"
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
			fmt.Printf("🔴 Tunnel deleted or expired on server\n")
			onTerminal()
			return
		}
		if status != "" && status != lastStatus {
			printTunnelStatusChange(status)
			lastStatus = status
		}
		if statusCode >= 400 {
			consecutiveFailures++
			if statusCode == 401 || statusCode == 403 {
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
func checkTunnelTerminalWithStatus(client *http.Client, serverURL, tunnelID, bearer string) (bool, int) {
	terminal, _, statusCode := checkTunnelTerminalWithStatusImpl(client, serverURL, tunnelID, bearer)
	return terminal, statusCode
}

func checkTunnelTerminal(client *http.Client, serverURL, tunnelID, bearer string) bool {
	terminal, _, _ := checkTunnelTerminalWithStatusImpl(client, serverURL, tunnelID, bearer)
	return terminal
}

func checkTunnelTerminalWithStatusImpl(client *http.Client, serverURL, tunnelID, bearer string) (bool, string, int) {
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

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return false, "", resp.StatusCode
	}
	status := tunnelStatusFromPayload(payload)

	if exists, ok := payload["exists"].(bool); ok && !exists {
		return true, status, resp.StatusCode
	}
	if status == StatusExpired {
		return true, status, resp.StatusCode
	}
	return false, status, resp.StatusCode
}

func tunnelStatusFromPayload(payload map[string]any) string {
	if status, ok := payload["status"].(string); ok && status != "" {
		return status
	}
	if tunnels, ok := payload["tunnels"].([]interface{}); ok && len(tunnels) > 0 {
		if t, ok := tunnels[0].(map[string]interface{}); ok {
			if status, ok := t["status"].(string); ok && status != "" {
				return status
			}
		}
	}
	return ""
}

func printTunnelStatusChange(status string) {
	switch status {
	case StatusExpired:
		return
	case statusActive:
		fmt.Printf("✅ Tunnel status changed to active on server\n")
	case statusPaused:
		fmt.Printf("⏸️ Tunnel status changed to paused on server\n")
	case statusNotActive:
		fmt.Printf("⚪ Tunnel status changed to not active on server\n")
	default:
		fmt.Printf("📨 Tunnel status changed on server: %s\n", status)
	}
}

func checkTunnelDeleted(client *http.Client, serverURL, tunnelID string) bool {
	return checkTunnelTerminal(client, serverURL, tunnelID, "")
}

func startControlMessageReader(
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
			var msg map[string]interface{}
			if err := conn.ReadJSON(&msg); err != nil {
				logWebSocketReadError(err)
				doneOnce.Do(func() { close(done) })
				return
			}
			if handleControlMessage(msg, ackCh, intervalCh, done, doneOnce, defaultWatchInterval, &lastStatus) {
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

func handleControlMessage(
	msg map[string]interface{},
	ackCh chan<- struct{},
	intervalCh chan<- time.Duration,
	done chan struct{},
	doneOnce *sync.Once,
	defaultWatchInterval time.Duration,
	lastStatus *string,
) bool {
	//nolint:errcheck // type assertion ok false is handled by default case
	msgType, _ := msg["type"].(string)
	switch msgType {
	case "pong":
		fmt.Printf("💓 Ping received at %s\n", time.Now().Format("15:04:05"))
	case "tunnel_closed":
		reason := extractTunnelCloseReason(msg)
		fmt.Printf("🔴 Tunnel closed on server (reason: %s)\n", reason)
		doneOnce.Do(func() { close(done) })
		return true
	case "tunnel_updated":
		if payload := extractPayload(msg); payload != nil {
			if status, ok := payload["status"].(string); ok {
				if status == StatusExpired {
					fmt.Printf("🔴 Tunnel expired on server\n")
					doneOnce.Do(func() { close(done) })
					return true
				}
				if lastStatus == nil || *lastStatus != status {
					printTunnelStatusChange(status)
					if lastStatus != nil {
						*lastStatus = status
					}
				}
			}
		}
	case "subscribed":
		notifyAckReceived(ackCh)
		updateFallbackInterval(intervalCh, defaultWatchInterval)
		fmt.Printf("📨 Message: %s\n", msgType)
	case "error":
		if payload := extractPayload(msg); payload != nil {
			if message, ok := payload["message"].(string); ok {
				fmt.Printf("❌ Error: %s\n", message)
			}
		}
	default:
		fmt.Printf("📨 Message: %s\n", msgType)
	}
	return false
}

func extractTunnelCloseReason(msg map[string]interface{}) string {
	payload := extractPayload(msg)
	if payload == nil {
		return "unknown"
	}
	if reason, ok := payload["reason"].(string); ok && reason != "" {
		return reason
	}
	return "unknown"
}

func extractPayload(msg map[string]interface{}) map[string]interface{} {
	if payload, ok := msg["payload"].(map[string]interface{}); ok {
		return payload
	}
	return nil
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
