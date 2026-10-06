// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/xtaci/smux"

	"github.com/fortunnels/client/internal/config"
)

type stoppableTicker interface {
	Stop()
}

// createDataPlaneSession creates a WebSocket connection and smux session for data plane operations.
// Returns the session and a cleanup function that should be called when done.
func CreateDataPlaneSession(serverURL, tunnelID string, settings config.RuntimeSettings, dpAuthToken string) (*smux.Session, func(), error) {
	wsURL, origin, authToken, err := buildWebSocketURL(serverURL, tunnelID, dpAuthToken)
	if err != nil {
		return nil, nil, err
	}
	h := http.Header{}
	h.Set("Origin", origin)
	h.Set(headerDataPlaneAuth, authToken)
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

// EnsureSessionContext returns a live session while allowing terminal lifecycle
// cancellation to abort an in-flight dial or reconnect backoff.
func (m *Manager) EnsureSessionContext(ctx context.Context) (dataPlaneSession, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	backoff := m.boInit
	for attempt := 1; ; attempt++ {
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
		settings, handler := m.settings, m.lifecycleHandler
		m.mu.Unlock()
		if wsURL == "" {
			return nil, errors.New("invalid websocket url")
		}
		conn, resp, stopDialCancellation, err := dialWebSocketContext(ctx, wsURL, headers)
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		var resources *sessionResources
		if err == nil {
			resources, err = initializeSession(conn, settings, handler)
		}
		stopDialCancellation()
		if err == nil {
			m.mu.Lock()
			if ctxErr := ctx.Err(); ctxErr != nil {
				m.mu.Unlock()
				resources.close()
				return nil, ctxErr
			}
			if m.stopped {
				m.mu.Unlock()
				resources.close()
				return nil, errors.New("stopped")
			}
			if m.sess != nil && !m.sess.IsClosed() {
				sess := m.sess
				m.mu.Unlock()
				resources.close()
				return sess, nil
			}
			previous := m.detachResourcesLocked()
			m.conn, m.sess = resources.conn, resources.sess
			m.pingDone, m.pingTicker = resources.pingDone, resources.pingTicker
			m.lifecycleControl = resources.lifecycleControl
			m.mu.Unlock()
			previous.close()
			return resources.sess, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if m.isStopped() {
			return nil, errors.New("stopped")
		}
		wait := backoff
		log.Printf("data-plane dial failed (attempt %d, next retry in %s): %s", attempt, wait, redactDialError(err, resp))
		backoff = nextBackoff(backoff, m.boMax)
		if !sleepReconnectBackoffContext(ctx, m.isStopped, wait) {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			return nil, errors.New("stopped")
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
	wsURL, origin, authToken, err := buildWebSocketURL(m.serverURL, m.tunnelID, m.dpAuthToken)
	if err != nil {
		return "", http.Header{}
	}
	h := http.Header{}
	h.Set("Origin", origin)
	h.Set(headerDataPlaneAuth, authToken)
	return wsURL, h
}

type sessionResources struct {
	conn             dataPlaneConn
	sess             dataPlaneSession
	pingDone         chan struct{}
	pingTicker       stoppableTicker
	lifecycleControl io.Closer
}

func (r *sessionResources) close() {
	if r.pingDone != nil {
		close(r.pingDone)
	}
	if r.pingTicker != nil {
		r.pingTicker.Stop()
	}
	if r.lifecycleControl != nil {
		_ = r.lifecycleControl.Close()
	}
	if r.sess != nil {
		_ = r.sess.Close()
	}
	if r.conn != nil {
		_ = r.conn.Close()
	}
}

func initializeSession(conn *websocket.Conn, settings config.RuntimeSettings, handler LifecycleHandler) (*sessionResources, error) {
	sess, err := setupWSSmuxSession(conn, settings)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("smux client: %w", err)
	}

	resources := &sessionResources{conn: conn, sess: sess}
	if handler != nil {
		control, controlErr := openLifecycleControlStream(sess, handler)
		if controlErr != nil {
			resources.close()
			return nil, controlErr
		}
		resources.lifecycleControl = control
	}
	resources.pingDone = make(chan struct{})
	pingTicker := time.NewTicker(settings.PingInterval)
	resources.pingTicker = pingTicker
	StartPingLoop(resources.pingDone, conn, pingTicker, settings.PingTimeout)
	return resources, nil
}

var dialErrorURL = regexp.MustCompile(`(?:https?|wss?)://[^\s"'<>]+`)

func redactDialError(err error, resp *http.Response) string {
	if resp != nil {
		return resp.Status
	}
	if err == nil {
		return ""
	}
	return dialErrorURL.ReplaceAllStringFunc(err.Error(), func(raw string) string {
		u, parseErr := url.Parse(raw)
		if parseErr != nil {
			return strings.SplitN(raw, "?", 2)[0]
		}
		u.RawQuery, u.Fragment, u.RawFragment = "", "", ""
		u.ForceQuery = false
		return u.String()
	})
}

// detachResourcesLocked transfers sole teardown ownership out of manager state.
func (m *Manager) detachResourcesLocked() *sessionResources {
	r := &sessionResources{conn: m.conn, sess: m.sess, pingDone: m.pingDone, pingTicker: m.pingTicker, lifecycleControl: m.lifecycleControl}
	m.conn, m.sess, m.pingDone, m.pingTicker, m.lifecycleControl = nil, nil, nil, nil, nil
	return r
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
	resources := m.detachResourcesLocked()
	m.mu.Unlock()
	resources.close()
}

func (m *Manager) Close() {
	m.mu.Lock()
	m.stopped = true
	resources := m.detachResourcesLocked()
	m.mu.Unlock()
	resources.close()
}
