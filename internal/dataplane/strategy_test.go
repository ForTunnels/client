// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"errors"
	"testing"

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
			strategy := NewStrategy(tt.kind, tt.serverURL, tt.tunnelID, tt.authToken, tt.dst, tt.listen, runtime, enc)

			if strategy.Description == "" {
				t.Error("NewStrategy() should set Description")
			}
			if !contains(strategy.Description, tt.wantDesc) {
				t.Errorf("NewStrategy() Description = %q, want containing %q", strategy.Description, tt.wantDesc)
			}
			if strategy.RunningMessage == "" {
				t.Error("NewStrategy() should set RunningMessage")
			}
			if strategy.StoppedMessage == "" {
				t.Error("NewStrategy() should set StoppedMessage")
			}
			if strategy.ErrLabel == "" {
				t.Error("NewStrategy() should set ErrLabel")
			}
			if strategy.runner == nil {
				t.Error("NewStrategy() should set runner")
			}
		})
	}
}

func TestStrategyRun(t *testing.T) {
	t.Run("nil runner", func(t *testing.T) {
		strategy := Strategy{
			runner: nil,
		}
		err := strategy.Run()
		if err != nil {
			t.Errorf("Strategy.Run() with nil runner = %v, want nil", err)
		}
	})

	t.Run("runner returns error", func(t *testing.T) {
		expectedErr := errors.New("test error")
		strategy := Strategy{
			runner: func() error {
				return expectedErr
			},
		}
		err := strategy.Run()
		if err != expectedErr {
			t.Errorf("Strategy.Run() = %v, want %v", err, expectedErr)
		}
	})

	t.Run("runner returns nil", func(t *testing.T) {
		strategy := Strategy{
			runner: func() error {
				return nil
			},
		}
		err := strategy.Run()
		if err != nil {
			t.Errorf("Strategy.Run() = %v, want nil", err)
		}
	})
}

func TestSimpleStrategy(t *testing.T) {
	description := "test description"
	running := "running message"
	stopped := "stopped message"
	errLabel := "error label"
	runnerErr := errors.New("runner error")

	runner := func() error {
		return runnerErr
	}

	strategy := simpleStrategy(description, running, stopped, errLabel, runner)

	if strategy.Description != description {
		t.Errorf("simpleStrategy() Description = %q, want %q", strategy.Description, description)
	}
	if strategy.RunningMessage != running {
		t.Errorf("simpleStrategy() RunningMessage = %q, want %q", strategy.RunningMessage, running)
	}
	if strategy.StoppedMessage != stopped {
		t.Errorf("simpleStrategy() StoppedMessage = %q, want %q", strategy.StoppedMessage, stopped)
	}
	if strategy.ErrLabel != errLabel {
		t.Errorf("simpleStrategy() ErrLabel = %q, want %q", strategy.ErrLabel, errLabel)
	}
	if strategy.runner == nil {
		t.Error("simpleStrategy() should set runner")
	}

	err := strategy.Run()
	if err != runnerErr {
		t.Errorf("simpleStrategy() runner = %v, want %v", err, runnerErr)
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
