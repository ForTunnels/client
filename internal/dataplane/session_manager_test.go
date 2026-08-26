// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/xtaci/smux"

	"github.com/fortunnels/client/internal/config"
)

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

	_, err := mgr.EnsureSession()
	require.Error(t, err)
	require.Equal(t, "stopped", err.Error())
}

func TestManagerEnsureSession_ReleasesLockDuringBackoff(t *testing.T) {
	mgr := NewManager("http://127.0.0.1:1", "tunnel-123", "", 2*time.Second, 2*time.Second, config.RuntimeSettings{})
	go func() {
		_, _ = mgr.EnsureSession()
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
