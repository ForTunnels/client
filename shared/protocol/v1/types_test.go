package v1

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTunnelLimitIndicatorsJSON(t *testing.T) {
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	resetAt := now.Add(time.Minute)
	usedSec := int64(120)
	remainingSec := int64(480)
	totalSec := int64(600)
	usedBytes := int64(1024)
	remainingBytes := int64(4096)
	totalBytes := int64(5120)
	usedReq := int64(3)
	remainingReq := int64(7)
	totalReq := int64(10)

	tunnel := Tunnel{
		ID:       "abc123",
		Protocol: "http",
		LimitIndicators: &LimitIndicators{
			AsOf: now,
			Lifetime: LifetimeLimitIndicator{
				State:            LimitIndicatorStateLimited,
				UsedSeconds:      &usedSec,
				RemainingSeconds: &remainingSec,
				TotalSeconds:     &totalSec,
				ResetAt:          &resetAt,
			},
			Traffic: TrafficLimitIndicator{
				State:          LimitIndicatorStateLimited,
				UsedBytes:      &usedBytes,
				RemainingBytes: &remainingBytes,
				TotalBytes:     &totalBytes,
			},
			HTTPRPM: HTTPRPMLimitIndicator{
				State:             LimitIndicatorStateLimited,
				UsedRequests:      &usedReq,
				RemainingRequests: &remainingReq,
				TotalRequests:     &totalReq,
				ResetAt:           &resetAt,
			},
		},
	}

	raw, err := json.Marshal(tunnel)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))

	indicators, ok := decoded["limit_indicators"].(map[string]any)
	require.True(t, ok, "limit_indicators must be present")
	require.Contains(t, indicators, "as_of")
	require.Contains(t, indicators, "lifetime")
	require.Contains(t, indicators, "traffic")
	require.Contains(t, indicators, "http_rpm")

	lifetime, ok := indicators["lifetime"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "limited", lifetime["state"])
	require.Equal(t, float64(120), lifetime["used_seconds"])
	require.Equal(t, float64(480), lifetime["remaining_seconds"])
	require.Equal(t, float64(600), lifetime["total_seconds"])
	require.NotNil(t, lifetime["reset_at"])

	traffic, ok := indicators["traffic"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "limited", traffic["state"])
	require.Equal(t, float64(1024), traffic["used_bytes"])

	httpRPM, ok := indicators["http_rpm"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "limited", httpRPM["state"])
	require.Equal(t, float64(7), httpRPM["remaining_requests"])

	unavailable := Tunnel{
		ID: "x",
		LimitIndicators: &LimitIndicators{
			AsOf: now,
			Lifetime: LifetimeLimitIndicator{
				State:            LimitIndicatorStateUnavailable,
				UsedSeconds:      nil,
				RemainingSeconds: nil,
				TotalSeconds:     nil,
				ResetAt:          nil,
			},
			Traffic: TrafficLimitIndicator{State: LimitIndicatorStateUnavailable},
			HTTPRPM: HTTPRPMLimitIndicator{State: LimitIndicatorStateUnavailable},
		},
	}
	rawUnavailable, err := json.Marshal(unavailable)
	require.NoError(t, err)
	s := string(rawUnavailable)
	require.Contains(t, s, `"used_seconds":null`)
	require.Contains(t, s, `"remaining_requests":null`)
	require.NotContains(t, s, "owner_login")
	require.NotContains(t, s, "user_id")
	require.NotContains(t, s, "plan")
}
