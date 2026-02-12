// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/xtaci/smux"

	"github.com/fortunnels/client/internal/config"
	"github.com/fortunnels/client/shared/wsconn"
)

type Client struct {
	conn       *websocket.Conn
	sess       *smux.Session
	pingTicker *time.Ticker
	done       chan struct{}
}

func NewWSSmuxClient(serverURL, tunnelID string, settings config.RuntimeSettings) (*Client, error) {
	wsURL, _, err := buildWebSocketURL(serverURL, tunnelID)
	if err != nil {
		return nil, err
	}
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("ws dial: %w", err)
	}

	if rdErr := conn.SetReadDeadline(time.Now().Add(wsReadTimeout)); rdErr == nil {
		conn.SetPongHandler(func(string) error {
			//nolint:errcheck // pong handler best-effort deadline refresh
			_ = conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
			return nil
		})
	}

	done := make(chan struct{})
	pingTicker := time.NewTicker(settings.PingInterval)
	startPingLoop(done, conn, pingTicker, settings.PingTimeout)

	cfg := smux.DefaultConfig()
	cfg.KeepAliveInterval = settings.SmuxKeepAliveInterval
	cfg.KeepAliveTimeout = settings.SmuxKeepAliveTimeout

	sess, err := smux.Client(wsconn.NewWSConn(conn), cfg)
	if err != nil {
		pingTicker.Stop()
		close(done)
		conn.Close()
		return nil, fmt.Errorf("smux client: %w", err)
	}

	return &Client{
		conn:       conn,
		sess:       sess,
		pingTicker: pingTicker,
		done:       done,
	}, nil
}

func (c *Client) Close() {
	if c == nil {
		return
	}
	c.pingTicker.Stop()
	close(c.done)
	_ = c.sess.Close()
	c.conn.Close()
}

// Session exposes the underlying smux session.
func (c *Client) Session() *smux.Session { return c.sess }

// Conn exposes the underlying websocket connection.
func (c *Client) Conn() *websocket.Conn { return c.conn }

// createDataPlaneSession creates a WebSocket connection and smux session for data plane operations.
// Returns the session and a cleanup function that should be called when done.
func CreateDataPlaneSession(serverURL, tunnelID string, settings config.RuntimeSettings) (*smux.Session, func(), error) {
	wsURL, origin, err := buildWebSocketURL(serverURL, tunnelID)
	if err != nil {
		return nil, nil, err
	}
	h := http.Header{}
	h.Set("Origin", origin)
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, h)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		return nil, nil, fmt.Errorf("ws dial: %w", err)
	}

	// WS keepalive
	//nolint:errcheck // best-effort read deadline
	_ = conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
	conn.SetPongHandler(func(string) error {
		//nolint:errcheck // pong handler best-effort deadline refresh
		_ = conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
		return nil
	})
	pingDone := make(chan struct{})
	pingTicker := time.NewTicker(settings.PingInterval)
	startPingLoop(pingDone, conn, pingTicker, settings.PingTimeout)

	cfg := smux.DefaultConfig()
	cfg.KeepAliveInterval = settings.SmuxKeepAliveInterval
	cfg.KeepAliveTimeout = settings.SmuxKeepAliveTimeout

	sess, err := smux.Client(wsconn.NewWSConn(conn), cfg)
	if err != nil {
		pingTicker.Stop()
		close(pingDone)
		conn.Close()
		return nil, nil, fmt.Errorf("smux client: %w", err)
	}

	cleanup := func() {
		_ = sess.Close()
		close(pingDone)
		pingTicker.Stop()
		conn.Close()
	}

	return sess, cleanup, nil
}

// Reconnectable session manager ensures there is a live smux session and
// reconnects with exponential backoff on failures.
type Manager struct {
	serverURL string
	tunnelID  string
	mu        sync.Mutex
	conn      *websocket.Conn
	sess      *smux.Session
	stopped   bool
	boInit    time.Duration
	boMax     time.Duration
	settings  config.RuntimeSettings
}

func NewManager(serverURL, tunnelID string, boInit, boMax time.Duration, settings config.RuntimeSettings) *Manager {
	return &Manager{
		serverURL: serverURL,
		tunnelID:  tunnelID,
		boInit:    boInit,
		boMax:     boMax,
		settings:  settings,
	}
}

func (m *Manager) EnsureSession() (*smux.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return nil, errors.New("stopped")
	}
	if m.sess != nil && !m.sess.IsClosed() {
		return m.sess, nil
	}
	wsURL, headers := m.sessionDialParams()
	if wsURL == "" {
		return nil, errors.New("invalid websocket url")
	}
	backoff := m.boInit
	for {
		if m.stopped {
			return nil, errors.New("stopped")
		}
		conn, resp, err := websocket.DefaultDialer.Dial(wsURL, headers)
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		if err == nil {
			sess, initErr := m.initializeSession(conn)
			if initErr == nil {
				return sess, nil
			}
		}
		time.Sleep(backoff)
		backoff = nextBackoff(backoff, m.boMax)
	}
}

func (m *Manager) sessionDialParams() (string, http.Header) {
	wsURL, origin, err := buildWebSocketURL(m.serverURL, m.tunnelID)
	if err != nil {
		return "", http.Header{}
	}
	h := http.Header{}
	h.Set("Origin", origin)
	return wsURL, h
}

func (m *Manager) initializeSession(conn *websocket.Conn) (*smux.Session, error) {
	//nolint:errcheck // best-effort read deadline
	_ = conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
	conn.SetPongHandler(func(string) error {
		//nolint:errcheck // pong handler best-effort deadline refresh
		_ = conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
		return nil
	})

	cfg := smux.DefaultConfig()
	cfg.KeepAliveInterval = m.settings.SmuxKeepAliveInterval
	cfg.KeepAliveTimeout = m.settings.SmuxKeepAliveTimeout

	sess, err := smux.Client(wsconn.NewWSConn(conn), cfg)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("smux client: %w", err)
	}

	m.conn = conn
	m.sess = sess
	m.startSessionPing(conn)
	return sess, nil
}

func (m *Manager) startSessionPing(conn *websocket.Conn) {
	go func() {
		t := time.NewTicker(m.settings.PingInterval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				//nolint:errcheck // best-effort ping
				_ = conn.WriteControl(
					websocket.PingMessage,
					nil,
					time.Now().Add(m.settings.PingTimeout),
				)
			default:
				if m.sess == nil || m.sess.IsClosed() {
					return
				}
				time.Sleep(1 * time.Second)
			}
		}
	}()
}

func nextBackoff(current, limit time.Duration) time.Duration {
	next := current * 2
	if next > limit {
		return limit
	}
	return next
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped = true
	if m.sess != nil {
		_ = m.sess.Close()
	}
	if m.conn != nil {
		_ = m.conn.Close()
	}
	m.sess = nil
	m.conn = nil
}

func startPingLoop(
	done <-chan struct{},
	conn *websocket.Conn,
	ticker *time.Ticker,
	pingTimeout time.Duration,
) {
	go func() {
		for {
			select {
			case <-ticker.C:
				deadline := time.Now().Add(pingTimeout)
				//nolint:errcheck // best-effort ping
				_ = conn.WriteControl(websocket.PingMessage, nil, deadline)
			case <-done:
				return
			}
		}
	}()
}
