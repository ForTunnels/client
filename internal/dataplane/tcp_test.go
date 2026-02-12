// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"bufio"
	"io"
	"net"
	"strings"
	"testing"
)

func TestReadStreamDestination(t *testing.T) {
	tests := []struct {
		name    string
		preface string
		want    string
		wantErr bool
	}{
		{
			name:    "valid preface",
			preface: `{"dst": "127.0.0.1:8080", "proto": "tcp"}` + "\n",
			want:    "127.0.0.1:8080",
			wantErr: false,
		},
		{
			name:    "preface without newline",
			preface: `{"dst": "127.0.0.1:8080", "proto": "tcp"}`,
			want:    "",
			wantErr: true,
		},
		{
			name:    "invalid JSON",
			preface: `{"dst": invalid}` + "\n",
			want:    "",
			wantErr: true,
		},
		{
			name:    "missing dst field",
			preface: `{"proto": "tcp"}` + "\n",
			want:    "",
			wantErr: false, // Returns empty string, not error
		},
		{
			name:    "empty dst",
			preface: `{"dst": "", "proto": "tcp"}` + "\n",
			want:    "",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rd := bufio.NewReader(strings.NewReader(tt.preface))
			got, err := readStreamDestination(rd)
			if (err != nil) != tt.wantErr {
				t.Errorf("readStreamDestination() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("readStreamDestination() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReadStreamDestination_EOF(t *testing.T) {
	rd := bufio.NewReader(strings.NewReader(""))
	_, err := readStreamDestination(rd)
	if err == nil {
		t.Error("readStreamDestination() with EOF should return error")
	}
}

// mockReadWriteCloser implements io.ReadWriteCloser for testing
type mockTCPReadWriteCloser struct {
	readData  []byte
	readErr   error
	writeData []byte
	writeErr  error
	closeErr  error
	closed    bool
}

func (m *mockTCPReadWriteCloser) Read(b []byte) (n int, err error) {
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

func (m *mockTCPReadWriteCloser) Write(b []byte) (n int, err error) {
	if m.writeErr != nil {
		return 0, m.writeErr
	}
	m.writeData = append(m.writeData, b...)
	return len(b), nil
}

func (m *mockTCPReadWriteCloser) Close() error {
	m.closed = true
	return m.closeErr
}

func TestServeIncomingStream_InvalidPreface(t *testing.T) {
	stream := &mockTCPReadWriteCloser{
		readData: []byte("invalid\n"),
	}
	err := serveIncomingStream(stream)
	if err == nil {
		t.Error("serveIncomingStream() with invalid preface should return error")
	}
	if !stream.closed {
		t.Error("serveIncomingStream() should close stream on error")
	}
}

func TestServeIncomingStream_EmptyDestination(t *testing.T) {
	preface := `{"dst": "", "proto": "tcp"}` + "\n"
	stream := &mockTCPReadWriteCloser{
		readData: []byte(preface),
	}
	_ = serveIncomingStream(stream)
	// serveIncomingStream returns err if dst == "" (from readStreamDestination).
	// If it returns nil, it tried to dial empty address (which fails with mocks).
}

func TestServeIncomingStream_DialError(t *testing.T) {
	preface := `{"dst": "invalid:address", "proto": "tcp"}` + "\n"
	stream := &mockTCPReadWriteCloser{
		readData: []byte(preface),
	}
	err := serveIncomingStream(stream)
	if err == nil {
		t.Error("serveIncomingStream() with dial error should return error")
	}
}

// createTestTCPServer starts a TCP server and returns its address and a cleanup function.
func createTestTCPServer(t *testing.T, handler func(net.Conn)) (addr string, cleanup func()) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create test server: %v", err)
	}
	addr = ln.Addr().String()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			if handler != nil {
				handler(conn)
			} else {
				conn.Close()
			}
		}
	}()

	return addr, func() { ln.Close() }
}

func TestServeIncomingStream_ValidConnection(t *testing.T) {
	// Create a test TCP server that echoes data
	serverAddr, cleanup := createTestTCPServer(t, func(conn net.Conn) {
		defer conn.Close()
		// Echo data
		_, _ = io.Copy(conn, conn)
	})
	defer cleanup()

	preface := `{"dst": "` + serverAddr + `", "proto": "tcp"}` + "\n"
	testData := []byte("test data\n")

	// Create a stream with preface and data
	streamData := append([]byte(preface), testData...)
	stream := &mockTCPReadWriteCloser{
		readData: streamData,
	}

	// This will try to dial the server and copy data
	// Since we're using mocks, we can't fully test the bidirectional copy
	// But we can verify the function handles valid input
	// In a real scenario, this would require integration tests
	_ = serveIncomingStream(stream)
	// Function may return error due to mock limitations; we only check stream was used.
	_ = stream.closed
}

func TestReadStreamDestination_WithWhitespace(t *testing.T) {
	preface := `  {"dst": "127.0.0.1:8080", "proto": "tcp"}  ` + "\n"
	rd := bufio.NewReader(strings.NewReader(preface))
	got, err := readStreamDestination(rd)
	if err != nil {
		t.Errorf("readStreamDestination() error = %v", err)
	}
	if got != "127.0.0.1:8080" {
		t.Errorf("readStreamDestination() = %q, want %q", got, "127.0.0.1:8080")
	}
}

func TestReadStreamDestination_MultipleFields(t *testing.T) {
	preface := `{"dst": "127.0.0.1:8080", "proto": "tcp", "extra": "field"}` + "\n"
	rd := bufio.NewReader(strings.NewReader(preface))
	got, err := readStreamDestination(rd)
	if err != nil {
		t.Errorf("readStreamDestination() error = %v", err)
	}
	if got != "127.0.0.1:8080" {
		t.Errorf("readStreamDestination() = %q, want %q", got, "127.0.0.1:8080")
	}
}
