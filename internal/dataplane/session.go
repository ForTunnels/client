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
)

type Client struct {
	conn       *websocket.Conn
	sess       *smux.Session
	pingTicker *time.Ticker
	done       chan struct{}
	closeOnce  sync.Once
}

func NewWSSmuxClient(serverURL, tunnelID string, settings config.RuntimeSettings, dpAuthToken string) (*Client, error) {
	wsURL, _, err := buildWebSocketURL(serverURL, tunnelID, dpAuthToken)
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

	done := make(chan struct{})
	pingTicker := time.NewTicker(settings.PingInterval)
	StartPingLoop(done, conn, pingTicker, settings.PingTimeout)

	sess, err := setupWSSmuxSession(conn, settings)
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
	c.closeOnce.Do(func() {
		if c.pingTicker != nil {
			c.pingTicker.Stop()
		}
		if c.done != nil {
			close(c.done)
		}
		if c.sess != nil {
			_ = c.sess.Close()
		}
		if c.conn != nil {
			c.conn.Close()
		}
		c.pingTicker = nil
		c.done = nil
		c.sess = nil
		c.conn = nil
	})
}

// Session exposes the underlying smux session.
func (c *Client) Session() *smux.Session { return c.sess }

// Conn exposes the underlying websocket connection.
func (c *Client) Conn() *websocket.Conn { return c.conn }

// createDataPlaneSession creates a WebSocket connection and smux session for data plane operations.
// Returns the session and a cleanup function that should be called when done.
func CreateDataPlaneSession(serverURL, tunnelID string, settings config.RuntimeSettings, dpAuthToken string) (*smux.Session, func(), error) {
	wsURL, origin, err := buildWebSocketURL(serverURL, tunnelID, dpAuthToken)
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

	pingDone := make(chan struct{})
	pingTicker := time.NewTicker(settings.PingInterval)
	StartPingLoop(pingDone, conn, pingTicker, settings.PingTimeout)

	sess, err := setupWSSmuxSession(conn, settings)
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
	serverURL   string
	tunnelID    string
	dpAuthToken string
	mu          sync.Mutex
	conn        *websocket.Conn
	sess        *smux.Session
	pingDone    chan struct{}
	pingTicker  *time.Ticker
	stopped     bool
	boInit      time.Duration
	boMax       time.Duration
	settings    config.RuntimeSettings
}

func NewManager(serverURL, tunnelID, dpAuthToken string, boInit, boMax time.Duration, settings config.RuntimeSettings) *Manager {
	return &Manager{
		serverURL:   serverURL,
		tunnelID:    tunnelID,
		dpAuthToken: dpAuthToken,
		boInit:      boInit,
		boMax:       boMax,
		settings:    settings,
	}
}

func (m *Manager) EnsureSession() (*smux.Session, error) {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return nil, errors.New("stopped")
	}
	if m.sess != nil && !m.sess.IsClosed() {
		sess := m.sess
		m.mu.Unlock()
		return sess, nil
	}
	wsURL, headers := m.sessionDialParams()
	if wsURL == "" {
		m.mu.Unlock()
		return nil, errors.New("invalid websocket url")
	}
	backoff := m.boInit
	for {
		if m.stopped {
			m.mu.Unlock()
			return nil, errors.New("stopped")
		}
		conn, resp, err := websocket.DefaultDialer.Dial(wsURL, headers)
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		if err == nil {
			sess, initErr := m.initializeSession(conn)
			if initErr == nil {
				m.mu.Unlock()
				return sess, nil
			}
		}
		wait := backoff
		backoff = nextBackoff(backoff, m.boMax)
		m.mu.Unlock()
		sleepReconnectBackoff(m.isStopped, wait)
		m.mu.Lock()
		if m.sess != nil && !m.sess.IsClosed() {
			sess := m.sess
			m.mu.Unlock()
			return sess, nil
		}
	}
}

func (m *Manager) isStopped() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopped
}

func (m *Manager) sessionDialParams() (string, http.Header) {
	wsURL, origin, err := buildWebSocketURL(m.serverURL, m.tunnelID, m.dpAuthToken)
	if err != nil {
		return "", http.Header{}
	}
	h := http.Header{}
	h.Set("Origin", origin)
	return wsURL, h
}

func (m *Manager) initializeSession(conn *websocket.Conn) (*smux.Session, error) {
	sess, err := setupWSSmuxSession(conn, m.settings)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("smux client: %w", err)
	}

	m.conn = conn
	m.sess = sess
	if m.pingDone != nil {
		close(m.pingDone)
	}
	if m.pingTicker != nil {
		m.pingTicker.Stop()
	}
	m.pingDone = make(chan struct{})
	m.pingTicker = time.NewTicker(m.settings.PingInterval)
	StartPingLoop(m.pingDone, conn, m.pingTicker, m.settings.PingTimeout)
	return sess, nil
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
	if m.pingDone != nil {
		close(m.pingDone)
		m.pingDone = nil
	}
	if m.pingTicker != nil {
		m.pingTicker.Stop()
		m.pingTicker = nil
	}
	if m.sess != nil {
		_ = m.sess.Close()
	}
	if m.conn != nil {
		_ = m.conn.Close()
	}
	m.sess = nil
	m.conn = nil
}
