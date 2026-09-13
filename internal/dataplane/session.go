// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
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
	pingTicker stoppableTicker
	done       chan struct{}
	closeOnce  sync.Once
}

type stoppableTicker interface {
	Stop()
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

	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			_ = sess.Close()
			close(pingDone)
			pingTicker.Stop()
			conn.Close()
		})
	}

	return sess, cleanup, nil
}

// Reconnectable session manager ensures there is a live smux session and
// reconnects with exponential backoff on failures.
type Manager struct {
	serverURL        string
	tunnelID         string
	dpAuthToken      string
	mu               sync.Mutex
	conn             dataPlaneConn
	sess             dataPlaneSession
	pingDone         chan struct{}
	pingTicker       stoppableTicker
	lifecycleHandler LifecycleHandler
	lifecycleControl io.Closer
	stopped          bool
	boInit           time.Duration
	boMax            time.Duration
	settings         config.RuntimeSettings
}

// SetLifecycleHandler enables the client-opened smux control stream used for
// terminal tunnel events. Set it before the first EnsureSession call.
func (m *Manager) SetLifecycleHandler(handler LifecycleHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lifecycleHandler = handler
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

type dataPlaneConn interface {
	Close() error
}

type dataPlaneSession interface {
	AcceptStream() (*smux.Stream, error)
	OpenStream() (*smux.Stream, error)
	IsClosed() bool
	Close() error
}

func (m *Manager) EnsureSession() (dataPlaneSession, error) {
	return m.EnsureSessionContext(context.Background())
}

// EnsureSessionContext returns a live session while allowing terminal lifecycle
// cancellation to abort an in-flight dial or reconnect backoff.
func (m *Manager) EnsureSessionContext(ctx context.Context) (dataPlaneSession, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	if err := ctx.Err(); err != nil {
		m.mu.Unlock()
		return nil, err
	}
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
		if err := ctx.Err(); err != nil {
			m.mu.Unlock()
			return nil, err
		}
		if m.stopped {
			m.mu.Unlock()
			return nil, errors.New("stopped")
		}
		conn, resp, stopDialCancellation, err := dialWebSocketContext(ctx, wsURL, headers)
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		if err == nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				_ = conn.Close()
				stopDialCancellation()
				m.mu.Unlock()
				return nil, ctxErr
			}
			sess, initErr := m.initializeSession(conn)
			stopDialCancellation()
			if initErr == nil {
				m.mu.Unlock()
				return sess, nil
			}
		} else {
			stopDialCancellation()
		}
		wait := backoff
		backoff = nextBackoff(backoff, m.boMax)
		m.mu.Unlock()
		if !sleepReconnectBackoffContext(ctx, m.isStopped, wait) {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			return nil, errors.New("stopped")
		}
		m.mu.Lock()
		if m.sess != nil && !m.sess.IsClosed() {
			sess := m.sess
			m.mu.Unlock()
			return sess, nil
		}
	}
}

func dialWebSocketContext(
	ctx context.Context,
	wsURL string,
	headers http.Header,
) (*websocket.Conn, *http.Response, func(), error) {
	dialer := *websocket.DefaultDialer
	baseDial := dialer.NetDialContext
	if baseDial == nil {
		baseDial = (&net.Dialer{}).DialContext
	}
	dialFinished := make(chan struct{})
	var finishOnce sync.Once
	finish := func() { finishOnce.Do(func() { close(dialFinished) }) }
	dialer.NetDialContext = func(dialCtx context.Context, network, address string) (net.Conn, error) {
		conn, err := baseDial(dialCtx, network, address)
		if err != nil {
			return nil, err
		}
		go func() {
			select {
			case <-ctx.Done():
				_ = conn.Close()
			case <-dialFinished:
			}
		}()
		return conn, nil
	}
	conn, resp, err := dialer.DialContext(ctx, wsURL, headers)
	return conn, resp, finish, err
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
	if m.lifecycleHandler != nil {
		control, controlErr := openLifecycleControlStream(sess, m.lifecycleHandler)
		if controlErr != nil {
			_ = sess.Close()
			_ = conn.Close()
			m.conn = nil
			m.sess = nil
			return nil, controlErr
		}
		m.lifecycleControl = control
	}
	if m.pingDone != nil {
		close(m.pingDone)
	}
	if m.pingTicker != nil {
		m.pingTicker.Stop()
	}
	m.pingDone = make(chan struct{})
	pingTicker := time.NewTicker(m.settings.PingInterval)
	m.pingTicker = pingTicker
	StartPingLoop(m.pingDone, conn, pingTicker, m.settings.PingTimeout)
	return sess, nil
}

func nextBackoff(current, limit time.Duration) time.Duration {
	next := current * 2
	if next > limit {
		return limit
	}
	return next
}

// resetSession drops a failed smux session so the next EnsureSession call dials
// a fresh data-plane connection. AcceptStream can fail before smux reports the
// session as closed, so relying on IsClosed alone can leave the serve loop
// spinning forever on a session the server closed during pause.
func (m *Manager) resetSession() {
	m.mu.Lock()
	sess := m.sess
	conn := m.conn
	pingDone := m.pingDone
	pingTicker := m.pingTicker
	lifecycleControl := m.lifecycleControl
	m.sess = nil
	m.conn = nil
	m.pingDone = nil
	m.pingTicker = nil
	m.lifecycleControl = nil
	m.mu.Unlock()

	if pingDone != nil {
		close(pingDone)
	}
	if pingTicker != nil {
		pingTicker.Stop()
	}
	if lifecycleControl != nil {
		_ = lifecycleControl.Close()
	}
	if sess != nil {
		_ = sess.Close()
	}
	if conn != nil {
		_ = conn.Close()
	}
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
	if m.lifecycleControl != nil {
		_ = m.lifecycleControl.Close()
		m.lifecycleControl = nil
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
