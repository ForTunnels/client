// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/fortunnels/client/internal/config"
	sec "github.com/fortunnels/client/internal/security"
)

// mockCloser implements io.Closer for testing
type mockCloser struct {
	closeErr error
	closed   bool
}

func (m *mockCloser) Close() error {
	m.closed = true
	return m.closeErr
}

// mockConn implements net.Conn for testing
type mockConn struct {
	readData  []byte
	readErr   error
	writeData []byte
	writeErr  error
	closed    bool
}

func (m *mockConn) Read(b []byte) (n int, err error) {
	if m.readErr != nil {
		return 0, m.readErr
	}
	if len(m.readData) == 0 {
		return 0, io.EOF
	}
	n = copy(b, m.readData)
	m.readData = m.readData[n:]
	return n, nil
}

func (m *mockConn) Write(b []byte) (n int, err error) {
	if m.writeErr != nil {
		return 0, m.writeErr
	}
	m.writeData = append(m.writeData, b...)
	return len(b), nil
}

func (m *mockConn) Close() error {
	m.closed = true
	return nil
}

func (m *mockConn) LocalAddr() net.Addr                { return nil }
func (m *mockConn) RemoteAddr() net.Addr               { return nil }
func (m *mockConn) SetDeadline(_ time.Time) error      { return nil }
func (m *mockConn) SetReadDeadline(_ time.Time) error  { return nil }
func (m *mockConn) SetWriteDeadline(_ time.Time) error { return nil }

// mockReadWriteCloser implements io.ReadWriteCloser for testing
type mockReadWriteCloser struct {
	readData  []byte
	readErr   error
	writeData []byte
	writeErr  error
	closeErr  error
	closed    bool
}

func (m *mockReadWriteCloser) Read(b []byte) (n int, err error) {
	if m.readErr != nil {
		return 0, m.readErr
	}
	if len(m.readData) == 0 {
		return 0, io.EOF
	}
	n = copy(b, m.readData)
	m.readData = m.readData[n:]
	return n, nil
}

func (m *mockReadWriteCloser) Write(b []byte) (n int, err error) {
	if m.writeErr != nil {
		return 0, m.writeErr
	}
	m.writeData = append(m.writeData, b...)
	return len(b), nil
}

func (m *mockReadWriteCloser) Close() error {
	m.closed = true
	return m.closeErr
}

func TestSafeClose(t *testing.T) {
	tests := []struct {
		name    string
		closer  io.Closer
		wantErr bool
	}{
		{"nil closer", nil, false},
		{"valid closer", &mockCloser{}, false},
		{"closer with error", &mockCloser{closeErr: errors.New("close error")}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// SafeClose should not panic and should handle errors gracefully
			SafeClose(tt.closer)
			if tt.closer != nil {
				mc, ok := tt.closer.(*mockCloser)
				if ok && !mc.closed {
					t.Error("SafeClose() did not close the closer")
				}
			}
		})
	}
}

func TestIsClosedPipe(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"closed pipe error", errors.New("write: closed pipe"), true},
		{"closed pipe in message", errors.New("connection closed pipe"), true},
		{"no closed pipe", errors.New("some other error"), false},
		{"broken pipe (different)", errors.New("write: broken pipe"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isClosedPipe(tt.err)
			if result != tt.expected {
				t.Errorf("isClosedPipe() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestPipeStreams(t *testing.T) {
	t.Run("bidirectional copy", func(t *testing.T) {
		// Create two mock connections with data to exchange
		connA := &mockConn{
			readData: []byte("hello from A"),
		}
		connB := &mockReadWriteCloser{
			readData: []byte("hello from B"),
		}

		// PipeStreams should copy data in both directions
		PipeStreams(connA, connB)

		// Verify data was written
		if len(connA.writeData) == 0 && len(connB.writeData) == 0 {
			t.Error("PipeStreams() did not copy any data")
		}
	})

	t.Run("handles EOF", func(_ *testing.T) {
		connA := &mockConn{
			readErr: io.EOF,
		}
		connB := &mockReadWriteCloser{
			readErr: io.EOF,
		}

		// Should not panic on EOF
		PipeStreams(connA, connB)
	})

	t.Run("handles closed pipe error", func(_ *testing.T) {
		connA := &mockConn{
			readErr: errors.New("read: broken pipe"),
		}
		connB := &mockReadWriteCloser{
			readErr: errors.New("read: broken pipe"),
		}

		// Should not panic on closed pipe
		PipeStreams(connA, connB)
	})
}

func TestWrapClientStream(t *testing.T) {
	t.Run("no encryption", func(t *testing.T) {
		stream := &mockReadWriteCloser{}
		enc := config.EncryptionSettings{
			Enabled: false,
		}

		result := WrapClientStream(stream, "tunnel-123", enc)
		if result != stream {
			t.Error("WrapClientStream() should return original stream when encryption is disabled")
		}
	})

	t.Run("with encryption", func(t *testing.T) {
		stream := &mockReadWriteCloser{}
		enc := config.EncryptionSettings{
			Enabled: true,
			PSK:     "test-secret-key",
		}

		result := WrapClientStream(stream, "tunnel-123", enc)
		if result == stream {
			t.Error("WrapClientStream() should return wrapped stream when encryption is enabled")
		}

		// Verify it's a ClientAEAD
		_, ok := result.(*sec.ClientAEAD)
		if !ok {
			t.Error("WrapClientStream() should return ClientAEAD when encryption is enabled")
		}
	})

	t.Run("encryption with empty PSK", func(t *testing.T) {
		stream := &mockReadWriteCloser{}
		enc := config.EncryptionSettings{
			Enabled: true,
			PSK:     "",
		}

		result := WrapClientStream(stream, "tunnel-123", enc)
		// Should still wrap even with empty PSK (validation happens elsewhere)
		if result == stream {
			t.Error("WrapClientStream() should return wrapped stream even with empty PSK")
		}
	})
}
