// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package control

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fortunnels/client/internal/config"
	"github.com/fortunnels/client/internal/dataplane"
	protocolv1 "github.com/fortunnels/client/shared/protocol/v1"
	"github.com/fortunnels/client/shared/wsconn"
)

var debugLogging = strings.Contains(
	strings.ToLower(strings.TrimSpace(os.Getenv("LOG_LEVEL"))), "debug",
)

// logDebug logs at DEBUG level when LOG_LEVEL env contains "debug" or "DEBUG".
func logDebug(format string, args ...any) {
	if debugLogging {
		log.Printf("[DEBUG] "+format, args...)
	}
}

// authMode describes how the lifecycle poller authenticates to the server.

type Watcher struct {
	out        Output
	onTerminal func(error)
}

// TrafficQuotaError is terminal for the current CLI invocation. A new
// invocation may proceed after the allowance resets or the plan changes.
type TrafficQuotaError struct {
	ResetAt time.Time
}

func (e *TrafficQuotaError) Error() string { return MonthlyTrafficLimitMessage(e.ResetAt) }

func MonthlyTrafficLimitMessage(resetAt time.Time) string {
	if resetAt.IsZero() {
		return "Monthly traffic limit reached (incoming + outgoing). Change your plan. Reconnecting or creating another tunnel will not reset this account's allowance."
	}
	return "Monthly traffic limit reached (incoming + outgoing). Wait until " +
		resetAt.UTC().Format(time.RFC3339) +
		" or change your plan. Reconnecting or creating another tunnel will not reset this account's allowance."
}

func NewWatcher(out Output) *Watcher {
	if out == nil {
		out = StdOutput{}
	}
	return &Watcher{out: out}
}

func NewLifecycleWatcher(out Output, onTerminal func(error)) *Watcher {
	w := NewWatcher(out)
	w.onTerminal = onTerminal
	return w
}

// ConnectWebSocket connects a control-plane WebSocket and manages keepalive/watchers.
// ConnectWebSocketWithAuth connects a control-plane WebSocket with optional bearer token
// or session-cookie client for fallback tunnel polling. httpClient may be nil; when
// provided with a cookie jar (session auth), fallback HTTP polls use it for auth.
// RunLifecycleWatch feeds terminal control-plane events to the shared CLI
// coordinator. It owns the WebSocket until the command is canceled or the
// server sends a terminal event.
func RunLifecycleWatch(
	ctx context.Context,
	httpClient *http.Client,
	serverURL, tunnelID, bearer string,
	runtime config.RuntimeSettings,
	onTerminal func(error),
) {
	w := NewLifecycleWatcher(nil, onTerminal)
	backoff := lifecycleBackoffInitial
	for ctx.Err() == nil {
		terminal, watchErr := w.lifecycleWatchAttempt(ctx, httpClient, serverURL, tunnelID, bearer, runtime)
		if terminal || ctx.Err() != nil {
			return
		}
		if watchErr != nil {
			logDebug("lifecycle WebSocket unavailable; next retry in %s", backoff)
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		backoff = min(backoff*2, lifecycleBackoffMax)
	}
}

const (
	lifecycleBackoffInitial = time.Second
	lifecycleBackoffMax     = 30 * time.Second
	lifecycleReadTimeout    = 90 * time.Second
)

func (w *Watcher) lifecycleWatchAttempt(ctx context.Context, httpClient *http.Client, serverURL, tunnelID, bearer string, runtime config.RuntimeSettings) (bool, error) {
	wsURL, err := buildControlWebSocketURL(serverURL, tunnelID)
	if err != nil {
		return false, err
	}
	dialer := *websocket.DefaultDialer
	baseDial := dialer.NetDialContext
	if baseDial == nil {
		baseDial = (&net.Dialer{}).DialContext
	}
	stopDialCancellation := func() bool { return false }
	dialer.NetDialContext = func(dialCtx context.Context, network, addr string) (net.Conn, error) {
		dialConn, dialErr := baseDial(dialCtx, network, addr)
		if dialErr == nil {
			stopDialCancellation = context.AfterFunc(ctx, func() { _ = dialConn.Close() })
		}
		return dialConn, dialErr
	}
	conn, resp, err := dialer.DialContext(ctx, wsURL, wsDialHeaders(httpClient, serverURL, bearer))
	defer stopDialCancellation()
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		return false, err
	}
	defer conn.Close()
	conn.SetReadLimit(wsconn.MaxWebSocketMessageSize)
	if deadlineErr := conn.SetReadDeadline(time.Now().Add(lifecycleReadTimeout)); deadlineErr != nil {
		return false, deadlineErr
	}
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(lifecycleReadTimeout)) })
	ackCh := make(chan struct{}, 1)
	intervalCh := make(chan time.Duration, 1)
	done := make(chan struct{})
	var doneOnce sync.Once
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		select {
		case <-ctx.Done():
		case <-done:
		}
		_ = conn.Close()
	}()
	defer func() { doneOnce.Do(func() { close(done) }); <-closed }()
	pingInterval, pingTimeout := runtime.PingInterval, runtime.PingTimeout
	if pingInterval <= 0 {
		pingInterval = 30 * time.Second
	}
	if pingTimeout <= 0 {
		pingTimeout = 5 * time.Second
	}
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	dataplane.StartControlPingLoop(done, &doneOnce, conn, ticker, pingTimeout)
	lastStatus := statusActive
	for {
		var msg protocolv1.Envelope
		if readErr := conn.ReadJSON(&msg); readErr != nil {
			return false, readErr
		}
		if w.handleControlMessage(msg, ackCh, intervalCh, done, &doneOnce, runtime.WatchInterval, &lastStatus) {
			return true, nil
		}
	}
}

// ConnectWebSocketWithAuth connects a control-plane WebSocket with optional bearer token
// or session-cookie client for fallback tunnel polling. httpClient may be nil; when
// provided with a cookie jar (session auth), fallback HTTP polls use it for auth.
// startFallbackTunnelWatcherWithAuth runs a background goroutine that polls the server
// for tunnel terminal state. httpClient may be nil; when provided with a cookie jar
// (session auth), it is used for authenticated requests. bearer is used when non-empty.
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
// RunFallbackLifecyclePollerWithReason is the authenticated canonical-indicator
// fallback used by the CLI serving loops. The callback receives a non-nil
// TrafficQuotaError only for combined monthly traffic exhaustion.
// RunFallbackLifecyclePollerWithReasonContext is the cancellation-aware form
// used by serving commands. Polling stops as soon as another signal wins.
func RunFallbackLifecyclePollerWithReasonContext(
	ctx context.Context,
	httpClient *http.Client,
	serverURL, tunnelID, bearer string,
	onTerminal func(error),
	interval time.Duration,
) {
	client := ensurePollClient(httpClient)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			terminal, _, _, terminalErr := checkTunnelTerminalDetailedContext(ctx, client, serverURL, tunnelID, bearer)
			if terminal {
				onTerminal(terminalErr)
				return
			}
		}
	}
}

func checkTunnelTerminalDetailedContext(parent context.Context, client *http.Client, serverURL, tunnelID, bearer string) (terminal bool, status string, statusCode int, terminalErr error) {
	timeout := client.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", serverURL+"/api/tunnels?id="+tunnelID, http.NoBody)
	if err != nil {
		return false, "", 0, nil
	}
	if strings.TrimSpace(bearer) != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, "", 0, nil
	}
	defer resp.Body.Close()

	// 401/403 from GET /api/tunnels?id=<id> means tunnel removed or access revoked for this session/token.
	// Treat as terminal immediately; no failure counters or WARN spam.
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		logDebug("%d from GET /api/tunnels → terminal (tunnel removed or access revoked)", resp.StatusCode)
		return true, "", resp.StatusCode, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, "", resp.StatusCode, nil
	}

	var payload protocolv1.TunnelListResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return false, "", resp.StatusCode, nil
	}
	status = tunnelStatusFromPayload(payload)
	if quotaErr := trafficQuotaErrorFromPayload(payload, tunnelID); quotaErr != nil {
		return true, status, resp.StatusCode, quotaErr
	}

	if !payload.Exists {
		return true, status, resp.StatusCode, nil
	}
	if status == StatusExpired {
		return true, status, resp.StatusCode, nil
	}
	return false, status, resp.StatusCode, nil
}

func trafficQuotaErrorFromPayload(payload protocolv1.TunnelListResponse, tunnelID string) error {
	for i := range payload.Tunnels {
		tunnel := &payload.Tunnels[i]
		if tunnelID != "" && tunnel.ID != "" && tunnel.ID != tunnelID {
			continue
		}
		if tunnel.LimitIndicators == nil {
			continue
		}
		indicators := tunnel.LimitIndicators
		monthlyIndicators := []protocolv1.MonthlyEgressLimitIndicator{
			indicators.MonthlyTraffic,
			indicators.MonthlyEgress,
		}
		trafficExhausted := indicators.Traffic.State == protocolv1.LimitIndicatorStateExhausted
		monthlyExhausted := false
		resetAt := time.Time{}
		for _, monthly := range monthlyIndicators {
			if monthly.State != protocolv1.LimitIndicatorStateExhausted {
				continue
			}
			monthlyExhausted = true
			if monthly.ResetAt != nil {
				resetAt = monthly.ResetAt.UTC()
			}
			break
		}
		if !trafficExhausted && !monthlyExhausted {
			continue
		}
		if trafficExhausted && !monthlyExhausted {
			for _, monthly := range monthlyIndicators {
				if monthly.ResetAt == nil {
					continue
				}
				resetAt = monthly.ResetAt.UTC()
				break
			}
		}
		return &TrafficQuotaError{ResetAt: resetAt}
	}
	return nil
}

func tunnelStatusFromPayload(payload any) string {
	response, ok := payload.(protocolv1.TunnelListResponse)
	if !ok {
		return ""
	}
	if response.Status != "" {
		return response.Status
	}
	if len(response.Tunnels) > 0 && response.Tunnels[0].Status != "" {
		return response.Tunnels[0].Status
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
		payload := decodeTunnelClosePayload(msg)
		reason := payload.Reason
		logDebug("tunnel_closed reason=%s", reason)
		var terminalErr error
		if reason == protocolv1.ReasonMonthlyTraffic {
			terminalErr = TrafficQuotaErrorFromLifecycle(payload)
		} else {
			terminalErr = newTunnelRemovedError()
		}
		if w.onTerminal != nil {
			w.onTerminal(terminalErr)
		} else {
			w.out.Println(terminalErr.Error())
		}
		doneOnce.Do(func() { close(done) })
		return true
	case protocolv1.EventTunnelUpdated:
		var payload protocolv1.LifecycleEventPayload
		if err := msg.DecodePayload(&payload); err == nil {
			if payload.Status == StatusExpired {
				if w.onTerminal != nil {
					w.onTerminal(newTunnelRemovedError())
				} else {
					w.out.Println(MsgTunnelRemovedExiting)
				}
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

func decodeTunnelClosePayload(msg protocolv1.Envelope) protocolv1.LifecycleEventPayload {
	var payload protocolv1.LifecycleEventPayload
	if err := msg.DecodePayload(&payload); err != nil {
		logDebug("decode tunnel_closed payload: %v", err)
	}
	if payload.Reason == "" {
		payload.Reason = protocolv1.ReasonUnknown
	}
	return payload
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
