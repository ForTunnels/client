// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package support

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"
)

func TestIsConnRefused(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "syscall.ECONNREFUSED in OpError",
			err:      &net.OpError{Err: &os.SyscallError{Err: syscall.ECONNREFUSED}},
			expected: true,
		},
		{
			name:     "url.Error wrapping OpError with ECONNREFUSED",
			err:      &url.Error{Err: &net.OpError{Err: &os.SyscallError{Err: syscall.ECONNREFUSED}}},
			expected: true,
		},
		{
			name:     "wrapped url.Error with ECONNREFUSED",
			err:      fmt.Errorf("dial failed: %w", &url.Error{Err: &net.OpError{Err: &os.SyscallError{Err: syscall.ECONNREFUSED}}}),
			expected: true,
		},
		{
			name:     "string contains connection refused",
			err:      errors.New("dial tcp: connection refused"),
			expected: true,
		},
		{
			name:     "string contains CONNECTION REFUSED (uppercase)",
			err:      errors.New("CONNECTION REFUSED"),
			expected: true,
		},
		{
			name:     "other error",
			err:      errors.New("some other error"),
			expected: false,
		},
		{
			name:     "OpError with different error",
			err:      &net.OpError{Err: errors.New("network unreachable")},
			expected: false,
		},
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsConnRefused(tt.err)
			if result != tt.expected {
				t.Errorf("IsConnRefused(%v) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

func TestIsDialTimeout(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "net.Error with Timeout",
			err:      &timeoutError{},
			expected: true,
		},
		{
			name:     "wrapped net.Error with Timeout",
			err:      fmt.Errorf("dial: %w", &timeoutError{}),
			expected: true,
		},
		{
			name:     "string contains timeout",
			err:      errors.New("dial timeout"),
			expected: true,
		},
		{
			name:     "string contains TIMEOUT (uppercase)",
			err:      errors.New("TIMEOUT"),
			expected: true,
		},
		{
			name:     "other error",
			err:      errors.New("some other error"),
			expected: false,
		},
		{
			name:     "net.Error without Timeout",
			err:      &nonTimeoutError{},
			expected: false,
		},
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsDialTimeout(tt.err)
			if result != tt.expected {
				t.Errorf("IsDialTimeout(%v) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

// timeoutError implements net.Error with Timeout() returning true
type timeoutError struct{}

func (e *timeoutError) Error() string   { return "timeout" }
func (e *timeoutError) Timeout() bool   { return true }
func (e *timeoutError) Temporary() bool { return false }

// nonTimeoutError implements net.Error with Timeout() returning false
type nonTimeoutError struct{}

func (e *nonTimeoutError) Error() string   { return "network error" }
func (e *nonTimeoutError) Timeout() bool   { return false }
func (e *nonTimeoutError) Temporary() bool { return false }

func TestHandleTunnelCreationError(t *testing.T) {
	serverURL := "http://127.0.0.1:8080"

	t.Run("conn refused error", func(t *testing.T) {
		err := HandleTunnelCreationError(
			&net.OpError{Err: &os.SyscallError{Err: syscall.ECONNREFUSED}},
			serverURL,
		)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "Unable to connect to server") {
			t.Errorf("unexpected message: %v", err)
		}
	})

	t.Run("wrapped conn refused error", func(t *testing.T) {
		inner := &net.OpError{Err: &os.SyscallError{Err: syscall.ECONNREFUSED}}
		err := HandleTunnelCreationError(fmt.Errorf("wrap: %w", inner), serverURL)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "Unable to connect to server") {
			t.Errorf("unexpected message: %v", err)
		}
	})

	t.Run("dial timeout error", func(t *testing.T) {
		err := HandleTunnelCreationError(&timeoutError{}, serverURL)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "Unable to connect to server") {
			t.Errorf("unexpected message: %v", err)
		}
	})

	t.Run("other error", func(t *testing.T) {
		err := HandleTunnelCreationError(errors.New("some other error"), serverURL)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "Failed to create tunnel") {
			t.Errorf("unexpected message: %v", err)
		}
	})

	t.Run("nil error", func(t *testing.T) {
		err := HandleTunnelCreationError(nil, serverURL)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unknown error") {
			t.Errorf("unexpected message: %v", err)
		}
	})
}
