// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/xtaci/smux"

	"github.com/fortunnels/client/internal/config"
)

func heldManagerUpgrade(t *testing.T) (*httptest.Server, <-chan struct{}, <-chan struct{}, func()) {
	t.Helper()
	requested, closed, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requested)
		<-release
		defer close(closed)
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(func() { unblock(); server.Close() })
	return server, requested, closed, unblock
}

func managerTestSettings() config.RuntimeSettings {
	return config.RuntimeSettings{PingInterval: time.Hour, PingTimeout: time.Second, SmuxKeepAliveInterval: time.Hour, SmuxKeepAliveTimeout: 2 * time.Hour}
}

func TestManagerEnsureSession_LockFreeDuringDial(t *testing.T) {
	server, requested, _, release := heldManagerUpgrade(t)
	mgr := NewManager(server.URL, "t1", "auth", time.Millisecond, time.Second, managerTestSettings())
	defer mgr.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := mgr.EnsureSessionContext(ctx); done <- err }()
	select {
	case <-requested:
	case <-time.After(time.Second):
		t.Fatal("upgrade was not requested")
	}
	locked := mgr.mu.TryLock()
	if locked {
		mgr.mu.Unlock()
	}
	require.True(t, locked, "mutex held across WebSocket handshake")
	cancel()
	release()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("dial did not cancel")
	}
}

func TestManagerEnsureSession_CloseDuringDialDiscardsSession(t *testing.T) {
	server, requested, closed, release := heldManagerUpgrade(t)
	mgr := NewManager(server.URL, "t1", "auth", time.Millisecond, time.Second, managerTestSettings())
	defer mgr.Close()
	done := make(chan error, 1)
	go func() { _, err := mgr.EnsureSessionContext(context.Background()); done <- err }()
	select {
	case <-requested:
	case <-time.After(time.Second):
		t.Fatal("upgrade was not requested")
	}
	stopped := make(chan struct{})
	go func() { mgr.Close(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Close blocked on in-flight dial")
	}
	release()
	select {
	case err := <-done:
		require.EqualError(t, err, "stopped")
	case <-time.After(2 * time.Second):
		t.Fatal("new session was not discarded")
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("discarded connection was not closed")
	}
	mgr.mu.Lock()
	session := mgr.sess
	mgr.mu.Unlock()
	require.Nil(t, session)
}

func TestManagerEnsureSession_DialFailureLogOmitsAuthToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer server.Close()
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	mgr := NewManager(server.URL, "t1", "private-auth-token", time.Second, time.Second, managerTestSettings())
	defer mgr.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := mgr.EnsureSessionContext(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Contains(t, output.String(), "503 Service Unavailable")
	require.NotContains(t, output.String(), "private-auth-token")
	for _, tt := range []struct {
		name     string
		err      error
		expected string
	}{
		{"url error", &url.Error{Op: "Get", URL: "wss://example.com/ws?auth=private-auth-token&mode=data", Err: io.EOF}, `Get "wss://example.com/ws": EOF`},
		{"plain error", errors.New("handshake unavailable? retry later"), "handshake unavailable? retry later"},
		{"multiple URLs", errors.New("dial https://a.example/ws?auth=private-auth-token then ws://b.example/ws?auth=private-auth-token"), "dial https://a.example/ws then ws://b.example/ws"},
	} {
		t.Run(tt.name, func(t *testing.T) { require.Equal(t, tt.expected, redactDialError(tt.err, nil)) })
	}
}

type resetSessionStub struct {
	closed atomic.Bool
}

func (*resetSessionStub) AcceptStream() (*smux.Stream, error) { return nil, errors.New("closed") }
func (*resetSessionStub) OpenStream() (*smux.Stream, error)   { return nil, errors.New("closed") }
func (*resetSessionStub) IsClosed() bool                      { return false }
func (s *resetSessionStub) Close() error {
	s.closed.Store(true)
	return nil
}

type resetConnStub struct {
	closed atomic.Bool
}

func (c *resetConnStub) Close() error {
	c.closed.Store(true)
	return nil
}

func TestManagerEnsureSession_StoppedAfterClose(t *testing.T) {
	mgr := NewManager("http://example.com", "tunnel-123", "", time.Millisecond, 10*time.Millisecond, config.RuntimeSettings{})
	mgr.Close()

	_, err := mgr.EnsureSessionContext(context.Background())
	require.Error(t, err)
	require.Equal(t, "stopped", err.Error())
}

func TestManagerEnsureSession_ReleasesLockDuringBackoff(t *testing.T) {
	mgr := NewManager("http://127.0.0.1:1", "tunnel-123", "", 2*time.Second, 2*time.Second, config.RuntimeSettings{})
	go func() {
		_, _ = mgr.EnsureSessionContext(context.Background())
	}()

	time.Sleep(300 * time.Millisecond)

	start := time.Now()
	acquired := mgr.mu.TryLock()
	elapsed := time.Since(start)
	if acquired {
		mgr.mu.Unlock()
	}
	mgr.Close()

	require.True(t, acquired, "lock should be available while reconnect sleeps")
	require.Less(t, elapsed, 50*time.Millisecond)
}

func TestManagerResetSession_DiscardsFailedSessionBeforeReconnect(t *testing.T) {
	mgr := NewManager("http://example.com", "tunnel-123", "", time.Millisecond, 10*time.Millisecond, config.RuntimeSettings{})
	sess := &resetSessionStub{}
	conn := &resetConnStub{}
	mgr.sess = sess
	mgr.conn = conn
	mgr.pingDone = make(chan struct{})
	mgr.pingTicker = time.NewTicker(time.Hour)

	mgr.resetSession()

	require.Nil(t, mgr.sess)
	require.Nil(t, mgr.conn)
	require.Nil(t, mgr.pingDone)
	require.Nil(t, mgr.pingTicker)
	require.True(t, sess.closed.Load())
	require.True(t, conn.closed.Load())
}
