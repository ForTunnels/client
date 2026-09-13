// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fortunnels/client/internal/config"
	protocolv1 "github.com/fortunnels/client/shared/protocol/v1"
)

func TestTrafficQuota_InBandFlatLifecyclePayload(t *testing.T) {
	resetAt := "2026-10-01T00:00:00Z"
	wire := `{"event":"tunnel_closed","tunnel_id":"tunnel-1","reason":"monthly_traffic","reset_at":"` + resetAt + `"}` + "\n"
	received := make(chan protocolv1.LifecycleEventPayload, 1)
	readLifecycleControlStream(strings.NewReader(wire), func(payload protocolv1.LifecycleEventPayload) {
		received <- payload
	})

	payload := <-received
	require.Equal(t, protocolv1.ReasonMonthlyTraffic, payload.Reason)
	require.NotNil(t, payload.ResetAt)
	require.Equal(t, resetAt, payload.ResetAt.UTC().Format(time.RFC3339))
}

func TestTrafficQuota_TransportCancellationNoRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var starts atomic.Int32
	strategy := contextStrategy("", "", "", func() error {
		t.Fatal("non-context runner must not be used")
		return nil
	}, func(ctx context.Context) error {
		starts.Add(1)
		<-ctx.Done()
		return ctx.Err()
	})

	done := make(chan error, 1)
	go func() { done <- strategy.RunContext(ctx) }()
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	require.Equal(t, int32(1), starts.Load())
}

func TestTrafficQuota_TransportCancellationStopsReconnectDial(t *testing.T) {
	requestStarted := make(chan struct{}, 1)
	releaseRequest := make(chan struct{})
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		requestStarted <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-releaseRequest:
		}
	}))
	defer func() {
		close(releaseRequest)
		server.Close()
	}()

	mgr := NewManager(server.URL, "tunnel-1", "signed-token", time.Second, time.Second, config.RuntimeSettings{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := mgr.EnsureSessionContext(ctx)
		done <- err
	}()

	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("initial data-plane dial did not start")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("quota cancellation did not abort the in-flight reconnect dial")
	}
	time.Sleep(100 * time.Millisecond)
	require.Equal(t, int32(1), attempts.Load(), "quota cancellation must not start another dial")
}
