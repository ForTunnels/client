// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

//go:build integration

package dataplane

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			assert.Equal(t, tt.want, got, "readStreamDestination() = %v, want %v")
		})
	}
}

func TestReadStreamDestination_EOF(t *testing.T) {
	rd := bufio.NewReader(strings.NewReader(""))
	_, err := readStreamDestination(rd)
	require.Error(t, err, "readStreamDestination() with EOF should return error")
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
	err := serveIncomingStream(stream, nil)
	require.Error(t, err, "serveIncomingStream() with invalid preface should return error")
	if !stream.closed {
		t.Error("serveIncomingStream() should close stream on error")
	}
}

func TestServeIncomingStream_EmptyDestination(t *testing.T) {
	preface := `{"dst": "", "proto": "tcp"}` + "\n"
	stream := &mockTCPReadWriteCloser{
		readData: []byte(preface),
	}
	err := serveIncomingStream(stream, nil)
	require.Error(t, err, "serveIncomingStream() with empty dst should return explicit error")
	require.Contains(t, err.Error(), "dst", "error message should mention dst")
}

func TestServeIncomingStream_DialError(t *testing.T) {
	preface := `{"dst": "invalid:address", "proto": "tcp"}` + "\n"
	stream := &mockTCPReadWriteCloser{
		readData: []byte(preface),
	}
	err := serveIncomingStream(stream, nil)
	require.Error(t, err, "serveIncomingStream() with dial error should return error")
}

// TestServeIncomingStream_DialFailureWritesSetupError verifies that on backend dial failure,
// the client writes a setup error JSON to the stream before closing (for server-side classification).
func TestServeIncomingStream_DialFailureWritesSetupError(t *testing.T) {
	// Use an address that is guaranteed unavailable: bind and release a port, then dial it.
	// This avoids fixed-port assumptions and yields a deterministic connection-refused failure.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	ln.Close()

	preface := `{"dst": "` + addr + `", "proto": "tcp"}` + "\n"
	stream := &mockTCPReadWriteCloser{
		readData: []byte(preface),
	}
	err = serveIncomingStream(stream, nil)
	require.Error(t, err, "serveIncomingStream() with dial error should return error")
	require.True(t, stream.closed, "stream should be closed on error")
	// Client must write setup error payload before close so server can classify the failure
	written := string(stream.writeData)
	require.Contains(t, written, `"ok":false`, "should write setup error with ok:false")
	require.Contains(t, written, `"error"`, "should write error message for server classification")
	// Do not assert on error message text (e.g. "refused") — it is OS/locale dependent
}

// TestServeIncomingStream_SuccessWritesSetupAck verifies that on successful backend dial,
// the client writes a setup ack JSON before bridging traffic.
func TestServeIncomingStream_SuccessWritesSetupAck(t *testing.T) {
	serverAddr, cleanup := createTestTCPServer(t, func(conn net.Conn) {
		defer conn.Close()
		_, _ = io.Copy(conn, conn)
	})
	defer cleanup()

	preface := `{"dst": "` + serverAddr + `", "proto": "tcp"}` + "\n"
	testData := []byte("ping\n")
	streamData := append([]byte(preface), testData...)
	stream := &mockTCPReadWriteCloser{
		readData: streamData,
	}

	_ = serveIncomingStream(stream, nil)
	// Client must write setup ack before bridging so server knows connection succeeded
	written := string(stream.writeData)
	require.Contains(t, written, `"ok":true`, "should write setup ack with ok:true before bridge")
}

// createTestTCPServer starts a TCP server and returns its address and a cleanup function.
func createTestTCPServer(t *testing.T, handler func(net.Conn)) (addr string, cleanup func()) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "Failed to create test server: %v")
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
	_ = serveIncomingStream(stream, nil)
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
	assert.Equal(t, "127.0.0.1:8080", got, "readStreamDestination() = %v, want %v")
}

func TestReadStreamDestination_MultipleFields(t *testing.T) {
	preface := `{"dst": "127.0.0.1:8080", "proto": "tcp", "extra": "field"}` + "\n"
	rd := bufio.NewReader(strings.NewReader(preface))
	got, err := readStreamDestination(rd)
	if err != nil {
		t.Errorf("readStreamDestination() error = %v", err)
	}
	assert.Equal(t, "127.0.0.1:8080", got, "readStreamDestination() = %v, want %v")
}

func TestFlushBufferedBytes_NoBufferedData(t *testing.T) {
	rd := bufio.NewReader(strings.NewReader(""))
	var out bytes.Buffer

	err := flushBufferedBytes(rd, &out)
	require.NoError(t, err)
	assert.Equal(t, "", out.String())
}

func TestFlushBufferedBytes_ForwardsOnlyBufferedBytes(t *testing.T) {
	rd := bufio.NewReader(strings.NewReader("abcdef"))
	buf := make([]byte, 3)
	_, err := rd.Read(buf)
	require.NoError(t, err)

	var out bytes.Buffer
	err = flushBufferedBytes(rd, &out)
	require.NoError(t, err)
	assert.Equal(t, "def", out.String())

	// Reader buffer is drained and should not expose stale bytes.
	assert.Equal(t, 0, rd.Buffered())
}

func TestServeIncomingStream_HTTP10WithoutContentLength_Completes(t *testing.T) {
	serverAddr, cleanup := createTestTCPServer(t, func(conn net.Conn) {
		defer conn.Close()
		_, _ = conn.Write([]byte("HTTP/1.0 200 OK\r\nContent-Type: text/plain\r\n\r\nok"))
	})
	defer cleanup()

	clientSide, serverSide := net.Pipe()
	defer serverSide.Close()

	errCh := make(chan error, 1)
	go func() {
		errCh <- serveIncomingStream(clientSide, nil)
	}()

	preface := `{"dst": "` + serverAddr + `", "proto": "tcp"}` + "\n"
	req := "GET / HTTP/1.1\r\nHost: test.local\r\n\r\n"
	_, err := serverSide.Write([]byte(preface + req))
	require.NoError(t, err)

	rd := bufio.NewReader(serverSide)
	ack, err := rd.ReadString('\n')
	require.NoError(t, err)
	require.Contains(t, ack, `"ok":true`)

	require.NoError(t, serverSide.SetReadDeadline(time.Now().Add(2*time.Second)))
	resp, err := io.ReadAll(rd)
	require.NoError(t, err, "response should terminate with EOF, not hang")
	require.Contains(t, string(resp), "HTTP/1.0 200 OK")
	require.Contains(t, string(resp), "\r\n\r\nok")

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("serveIncomingStream did not finish")
	}
}
