// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fortunnels/client/internal/config"
)

func TestNewStrategy(t *testing.T) {
	runtime := config.RuntimeSettings{
		PingInterval:          30 * 1000000000, // 30 seconds in nanoseconds
		PingTimeout:           5 * 1000000000,  // 5 seconds
		WatchInterval:         10 * 1000000000, // 10 seconds
		SmuxKeepAliveInterval: 30 * 1000000000,
		SmuxKeepAliveTimeout:  90 * 1000000000,
	}
	enc := config.EncryptionSettings{
		Enabled: false,
	}

	tests := []struct {
		name      string
		kind      string
		serverURL string
		tunnelID  string
		authToken string
		dst       string
		listen    string
		wantDesc  string
	}{
		{
			name:      "quic strategy",
			kind:      "quic",
			serverURL: "https://example.com",
			tunnelID:  "tunnel-123",
			authToken: "token",
			dst:       "127.0.0.1:8080",
			listen:    "127.0.0.1:9000",
			wantDesc:  "UDP over QUIC",
		},
		{
			name:      "dtls strategy",
			kind:      "dtls",
			serverURL: "https://example.com",
			tunnelID:  "tunnel-123",
			authToken: "token",
			dst:       "127.0.0.1:8080",
			listen:    "127.0.0.1:9000",
			wantDesc:  "UDP over DTLS",
		},
		{
			name:      "default ws strategy",
			kind:      "ws",
			serverURL: "https://example.com",
			tunnelID:  "tunnel-123",
			authToken: "token",
			dst:       "127.0.0.1:8080",
			listen:    "127.0.0.1:9000",
			wantDesc:  "UDP mode",
		},
		{
			name:      "empty kind defaults to ws",
			kind:      "",
			serverURL: "https://example.com",
			tunnelID:  "tunnel-123",
			authToken: "token",
			dst:       "127.0.0.1:8080",
			listen:    "127.0.0.1:9000",
			wantDesc:  "UDP mode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			strategy := NewStrategy(tt.kind, tt.serverURL, tt.tunnelID, tt.authToken, tt.dst, tt.listen, runtime, enc, time.Second, 30*time.Second)

			if strategy.Description == "" {
				t.Error("NewStrategy() should set Description")
			}
			if !contains(strategy.Description, tt.wantDesc) {
				t.Errorf("NewStrategy() Description = %q, want containing %q", strategy.Description, tt.wantDesc)
			}
			if strategy.RunningMessage == "" {
				t.Error("NewStrategy() should set RunningMessage")
			}
			if strategy.ErrLabel == "" {
				t.Error("NewStrategy() should set ErrLabel")
			}
			if strategy.contextRunner == nil {
				t.Error("NewStrategy() should set context runner")
			}
		})
	}
}

func TestRunReconnecting_RetriesTransientAndStopsOnPermanent(t *testing.T) {
	t.Run("pre-canceled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		attempts := 0
		err := runReconnecting(ctx, time.Second, time.Second, func(context.Context) error { attempts++; return nil })
		require.ErrorIs(t, err, context.Canceled)
		require.Zero(t, attempts)
	})
	t.Run("transient failures then success", func(t *testing.T) {
		attempts := 0
		err := runReconnecting(context.Background(), time.Millisecond, 2*time.Millisecond, func(context.Context) error {
			attempts++
			if attempts < 3 {
				return errors.New("transport failed")
			}
			return nil
		})
		require.NoError(t, err)
		require.Equal(t, 3, attempts)
	})
	t.Run("permanent failure", func(t *testing.T) {
		attempts := 0
		cause := errors.New("invalid listener")
		err := runReconnecting(context.Background(), time.Second, time.Second, func(context.Context) error {
			attempts++
			return fmt.Errorf("local setup: %w", permanentError{cause})
		})
		require.ErrorIs(t, err, cause)
		require.Equal(t, 1, attempts)
	})
	t.Run("cancel during backoff", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		attempted := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- runReconnecting(ctx, time.Hour, time.Hour, func(context.Context) error {
				close(attempted)
				return errors.New("transport failed")
			})
		}()
		<-attempted
		cancel()
		select {
		case err := <-done:
			require.ErrorIs(t, err, context.Canceled)
		case <-time.After(time.Second):
			t.Fatal("retry wait ignored cancellation")
		}
	})
}

func TestRunReconnecting_DTLSRemoteDNSFailuresRetry(t *testing.T) {
	previous := resolveDTLSServerAddr
	t.Cleanup(func() { resolveDTLSServerAddr = previous })
	for _, dnsErr := range []*net.DNSError{
		{Name: "remote.example", Err: "DNS timeout", IsTimeout: true, IsTemporary: true},
		{Name: "remote.example", Err: "server misbehaving", IsTemporary: true},
	} {
		t.Run(dnsErr.Err, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			attempts := 0
			resolveDTLSServerAddr = func(network, address string) (*net.UDPAddr, error) {
				require.Equal(t, "udp", network)
				require.Equal(t, "remote.example:4444", address)
				attempts++
				if attempts == 2 {
					cancel()
				}
				return nil, dnsErr
			}
			strategy := NewStrategy(
				"dtls", "https://remote.example", "t1", "auth", "127.0.0.1:53", "127.0.0.1:0",
				config.RuntimeSettings{DTLSPort: 4444}, config.EncryptionSettings{}, time.Millisecond, time.Millisecond,
			)
			err := strategy.RunContext(ctx)
			require.ErrorIs(t, err, context.Canceled)
			require.Equal(t, 2, attempts, "remote DNS failure must retry")
		})
	}
}

func TestRunReconnecting_DTLSInvalidServerConfigIsPermanent(t *testing.T) {
	previous := resolveDTLSServerAddr
	t.Cleanup(func() { resolveDTLSServerAddr = previous })
	resolveDTLSServerAddr = func(string, string) (*net.UDPAddr, error) {
		t.Fatal("remote DNS must not run before local configuration validation")
		return nil, errors.New("unexpected lookup")
	}
	for _, tt := range []struct{ name, serverURL, port, caPath string }{
		{"malformed URL", "://bad", "4444", ""},
		{"missing host", "https://", "4444", ""},
		{"unsupported scheme", "ftp://remote.example", "4444", ""},
		{"invalid port", "https://remote.example", "invalid-service", ""},
		{"missing CA", "https://remote.example", "4444", t.TempDir() + "/missing.pem"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			attempts := 0
			err := runReconnecting(context.Background(), time.Hour, time.Hour, func(ctx context.Context) error {
				attempts++
				return startDTLSDataPlaneUDPContext(ctx, tt.serverURL, tt.port, "t1", "auth", "127.0.0.1:53", "127.0.0.1:0", tt.caPath)
			})
			var permanent permanentError
			require.ErrorAs(t, err, &permanent)
			require.Equal(t, 1, attempts)
		})
	}
}

func TestStartDataPlaneUDPContext_ListenFailureIsPermanent(t *testing.T) {
	listener, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	err = StartDataPlaneUDPContext(context.Background(), "http://127.0.0.1:1", "t1", "127.0.0.1:53", listener.LocalAddr().String(), config.RuntimeSettings{}, config.EncryptionSettings{}, "auth")
	var permanent permanentError
	require.ErrorAs(t, err, &permanent)
	require.Contains(t, err.Error(), "listen udp")
	for _, kind := range []string{"ws", "quic", "dtls"} {
		t.Run(kind, func(t *testing.T) {
			strategy := NewStrategy(kind, "http://127.0.0.1:1", "t1", "auth", "127.0.0.1:53", listener.LocalAddr().String(), config.RuntimeSettings{}, config.EncryptionSettings{}, time.Hour, time.Hour)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var permanent permanentError
			require.ErrorAs(t, strategy.RunContext(ctx), &permanent)
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || substr == "" ||
		(len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr ||
			containsMiddle(s, substr))))
}

func containsMiddle(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
