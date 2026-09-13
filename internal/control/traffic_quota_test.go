package control

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	protocolv1 "github.com/fortunnels/client/shared/protocol/v1"
)

type quotaTestOutput struct {
	mu    sync.Mutex
	lines []string
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func (o *quotaTestOutput) Printf(format string, args ...any) {
	o.Println(fmt.Sprintf(format, args...))
}

func (o *quotaTestOutput) Println(args ...any) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lines = append(o.lines, fmt.Sprintln(args...))
}

func (o *quotaTestOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return strings.Join(o.lines, "")
}

func TestTrafficQuotaMessageIncludesResetWhenAvailable(t *testing.T) {
	resetAt := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	require.Equal(t,
		"Monthly traffic limit reached (incoming + outgoing). Wait until 2026-10-01T00:00:00Z or change your plan. Reconnecting or creating another tunnel will not reset this account's allowance.",
		MonthlyTrafficLimitMessage(resetAt),
	)
	require.Equal(t,
		"Monthly traffic limit reached (incoming + outgoing). Change your plan. Reconnecting or creating another tunnel will not reset this account's allowance.",
		MonthlyTrafficLimitMessage(time.Time{}),
	)
}

func TestTrafficQuotaCanonicalIndicatorIsTerminal(t *testing.T) {
	resetAt := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	payload := protocolv1.TunnelListResponse{
		Exists: true,
		Tunnels: []protocolv1.Tunnel{{
			ID: "tunnel-1",
			LimitIndicators: &protocolv1.LimitIndicators{
				Traffic: protocolv1.TrafficLimitIndicator{State: protocolv1.LimitIndicatorStateExhausted},
				MonthlyEgress: protocolv1.MonthlyEgressLimitIndicator{
					State:   protocolv1.LimitIndicatorStateLimited,
					ResetAt: &resetAt,
				},
			},
		}},
	}

	err := trafficQuotaErrorFromPayload(payload, "tunnel-1")
	var quotaErr *TrafficQuotaError
	require.ErrorAs(t, err, &quotaErr)
	require.True(t, quotaErr.ResetAt.Equal(resetAt))
	require.False(t, errors.Is(err, nil))
}

func TestTrafficQuota_TerminalMessageOnceAcrossSources(t *testing.T) {
	resetAt := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	out := &quotaTestOutput{}
	ctx, cancel := context.WithCancel(context.Background())
	coordinator := NewTerminalCoordinator(out, cancel)

	signals := []error{
		&TrafficQuotaError{ResetAt: resetAt},
		&TrafficQuotaError{ResetAt: resetAt},
		&TrafficQuotaError{ResetAt: resetAt},
	}
	var wg sync.WaitGroup
	for _, signal := range signals {
		wg.Add(1)
		go func(err error) {
			defer wg.Done()
			coordinator.Signal(err)
		}(signal)
	}
	wg.Wait()

	select {
	case <-coordinator.Done():
	case <-time.After(time.Second):
		t.Fatal("terminal coordinator did not close")
	}
	require.Error(t, coordinator.Err())
	require.True(t, IsReportedTerminalError(coordinator.Err()))
	require.Equal(t, 1, strings.Count(out.String(), "Monthly traffic limit reached"))
	require.ErrorIs(t, ctx.Err(), context.Canceled)
}

func TestTrafficQuota_AuthenticatedPollFallback(t *testing.T) {
	resetAt := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	seenAuth := make(chan string, 1)
	payload := fmt.Sprintf(
		`{"exists":true,"tunnels":[{"id":"tunnel-1","limit_indicators":`+
			`{"traffic":{"state":"exhausted"},"monthly_egress":{"state":"limited","reset_at":%q}}}]}`,
		resetAt.Format(time.RFC3339),
	)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		seenAuth <- r.Header.Get("Authorization")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(payload)),
			Request:    r,
		}, nil
	})}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	terminal := make(chan error, 1)
	go RunFallbackLifecyclePollerWithReasonContext(ctx, client, "https://server.test", "tunnel-1", "signed-token", func(err error) {
		terminal <- err
	}, time.Millisecond)

	select {
	case err := <-terminal:
		var quotaErr *TrafficQuotaError
		require.ErrorAs(t, err, &quotaErr)
		require.True(t, quotaErr.ResetAt.Equal(resetAt))
	case <-ctx.Done():
		t.Fatal("poller did not deliver terminal quota state")
	}
	require.Equal(t, "Bearer signed-token", <-seenAuth)
}

func TestTrafficQuota_LegacyWireCompatibility(t *testing.T) {
	resetAt := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	payload := protocolv1.LifecycleEventPayload{
		TunnelID: "tunnel-1",
		Reason:   protocolv1.ReasonMonthlyTraffic,
		ResetAt:  &resetAt,
	}
	msg := protocolv1.NewEnvelope(protocolv1.EventTunnelClosed, payload)
	terminal := make(chan error, 1)
	w := NewLifecycleWatcher(&quotaTestOutput{}, func(err error) { terminal <- err })
	done := make(chan struct{})
	var once sync.Once
	returned := w.handleControlMessage(msg, make(chan struct{}, 1), make(chan time.Duration, 1), done, &once, time.Second, nil)
	require.True(t, returned)
	var quotaErr *TrafficQuotaError
	require.ErrorAs(t, <-terminal, &quotaErr)
	require.True(t, quotaErr.ResetAt.Equal(resetAt))

	legacy := protocolv1.NewEnvelope(protocolv1.EventTunnelClosed, protocolv1.BuildTunnelClosedPayload("tunnel-1", protocolv1.ReasonDeleted))
	legacyTerminal := make(chan error, 1)
	w = NewLifecycleWatcher(&quotaTestOutput{}, func(err error) { legacyTerminal <- err })
	done = make(chan struct{})
	once = sync.Once{}
	require.True(t, w.handleControlMessage(legacy, make(chan struct{}, 1), make(chan time.Duration, 1), done, &once, time.Second, nil))
	require.EqualError(t, <-legacyTerminal, MsgTunnelRemovedExiting)
}
