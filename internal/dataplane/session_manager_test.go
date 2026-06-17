// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fortunnels/client/internal/config"
)

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
