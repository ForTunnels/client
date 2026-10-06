// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"

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

func TestWrapClientStream(t *testing.T) {
	t.Run("no encryption", func(t *testing.T) {
		stream := &mockReadWriteCloser{}
		enc := config.EncryptionSettings{
			Enabled: false,
		}

		result, err := WrapClientStream(stream, "tunnel-123", enc)
		assert.NoError(t, err)
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

		result, err := WrapClientStream(stream, "tunnel-123", enc)
		assert.NoError(t, err)
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

		result, err := WrapClientStream(stream, "tunnel-123", enc)
		assert.NoError(t, err)
		// Should still wrap even with empty PSK (validation happens elsewhere)
		if result == stream {
			t.Error("WrapClientStream() should return wrapped stream even with empty PSK")
		}
	})
}
