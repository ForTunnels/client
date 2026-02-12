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
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fortunnels/client/internal/config"
)

// ConnectWebSocket connects a control-plane WebSocket and manages keepalive/watchers.
func ConnectWebSocket(serverURL, tunnelID string, runtime config.RuntimeSettings) {
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
	startFallbackTunnelWatcher(serverURL, tunnelID, time.Second, intervalCh, done, &doneOnce)
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
	go func() {
		ticker := time.NewTicker(initialInterval)
		defer ticker.Stop()
		client := &http.Client{Timeout: 2 * time.Second}
		for {
			select {
			case <-ticker.C:
				if checkTunnelDeleted(client, serverURL, tunnelID) {
					fmt.Printf("🔴 Tunnel deleted on server\n")
					doneOnce.Do(func() { close(done) })
					return
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

func checkTunnelDeleted(client *http.Client, serverURL, tunnelID string) bool {
	timeout := client.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", serverURL+"/api/tunnels?id="+tunnelID, http.NoBody)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return false
	}
	if exists, ok := payload["exists"].(bool); ok {
		return !exists
	}
	return false
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
		for {
			var msg map[string]interface{}
			if err := conn.ReadJSON(&msg); err != nil {
				logWebSocketReadError(err)
				doneOnce.Do(func() { close(done) })
				return
			}
			if handleControlMessage(msg, ackCh, intervalCh, done, doneOnce, defaultWatchInterval) {
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
