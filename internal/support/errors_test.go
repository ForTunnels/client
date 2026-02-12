// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package support

import (
	"errors"
	"net"
	"net/url"
	"os"
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

func TestAs(t *testing.T) {
	t.Run("url.Error", func(t *testing.T) {
		uerr := &url.Error{Op: "GET", URL: "http://example.com", Err: errors.New("test")}
		var target *url.Error
		result := As(uerr, &target)
		if !result {
			t.Error("As() with url.Error should return true")
		}
		if target != uerr {
			t.Error("As() should set target to the error")
		}
	})

	t.Run("net.OpError", func(t *testing.T) {
		operr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("test")}
		var target *net.OpError
		result := As(operr, &target)
		if !result {
			t.Error("As() with net.OpError should return true")
		}
		if target != operr {
			t.Error("As() should set target to the error")
		}
	})

	t.Run("net.Error", func(t *testing.T) {
		nerr := &timeoutError{}
		var target net.Error
		result := As(nerr, &target)
		if !result {
			t.Error("As() with net.Error should return true")
		}
		if target != nerr {
			t.Error("As() should set target to the error")
		}
	})

	t.Run("non-matching type", func(t *testing.T) {
		err := errors.New("test")
		var target *url.Error
		result := As(err, &target)
		if result {
			t.Error("As() with non-matching type should return false")
		}
	})

	t.Run("nil error", func(t *testing.T) {
		var target *url.Error
		result := As(nil, &target)
		if result {
			t.Error("As() with nil error should return false")
		}
	})
}

func TestHandleTunnelCreationError(t *testing.T) {
	// Note: This function calls os.Exit(1), so we can't test it directly
	// We can only verify the logic paths that don't exit
	// In practice, this would be tested via integration tests or by mocking os.Exit

	t.Run("conn refused error", func(t *testing.T) {
		// This test verifies the function recognizes conn refused errors
		// Actual os.Exit behavior would be tested in integration tests
		err := &net.OpError{Err: &os.SyscallError{Err: syscall.ECONNREFUSED}}
		// We can't actually call HandleTunnelCreationError as it exits
		// But we can verify IsConnRefused works
		if !IsConnRefused(err) {
			t.Error("Expected IsConnRefused to return true")
		}
	})

	t.Run("dial timeout error", func(t *testing.T) {
		err := &timeoutError{}
		// Verify IsDialTimeout works
		if !IsDialTimeout(err) {
			t.Error("Expected IsDialTimeout to return true")
		}
	})

	t.Run("other error", func(t *testing.T) {
		err := errors.New("some other error")
		// Verify it's not conn refused or timeout
		if IsConnRefused(err) {
			t.Error("Expected IsConnRefused to return false")
		}
		if IsDialTimeout(err) {
			t.Error("Expected IsDialTimeout to return false")
		}
	})
}
