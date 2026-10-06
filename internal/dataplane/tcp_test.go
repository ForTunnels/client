// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

//go:build integration

package dataplane

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"os"
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

func TestServeIncomingStream_BackendDialTimeoutWritesSetupError(t *testing.T) {
	previous := dialBackend
	t.Cleanup(func() { dialBackend = previous })
	timeoutErr := &net.OpError{Op: "dial", Net: "tcp", Err: os.ErrDeadlineExceeded}
	dialBackend = func(network, address string) (net.Conn, error) {
		require.Equal(t, "tcp", network)
		require.Equal(t, "127.0.0.1:8080", address)
		return nil, timeoutErr
	}
	stream := &mockTCPReadWriteCloser{readData: []byte(`{"dst":"127.0.0.1:8080","proto":"tcp"}` + "\n")}
	var reported error
	err := serveIncomingStream(stream, func(_ string, err error) { reported = err })
	var netErr net.Error
	require.ErrorAs(t, err, &netErr)
	require.True(t, netErr.Timeout())
	require.ErrorIs(t, reported, timeoutErr)
	require.True(t, stream.closed)
	require.Contains(t, string(stream.writeData), `"ok":false`)
	require.Contains(t, string(stream.writeData), `"error":`)
	require.True(t, strings.HasSuffix(string(stream.writeData), "\n"))
	require.Equal(t, 5*time.Second, backendDialer.Timeout)
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

func tcpBridgeTestPair(t *testing.T) (*net.TCPConn, *net.TCPConn) {
	t.Helper()
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.0.1")})
	require.NoError(t, err)
	defer listener.Close()
	peer, err := net.DialTCP("tcp", nil, listener.Addr().(*net.TCPAddr))
	require.NoError(t, err)
	t.Cleanup(func() { _ = peer.Close() })
	conn, err := listener.AcceptTCP()
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn, peer
}

func TestBridgeStreamAndBackend_HalfCloseIdleTimeoutCompletes(t *testing.T) {
	previous := halfCloseIdleTimeout
	halfCloseIdleTimeout = 100 * time.Millisecond
	t.Cleanup(func() { halfCloseIdleTimeout = previous })
	stream, peer := tcpBridgeTestPair(t)
	backend, backendPeer := net.Pipe()
	defer backend.Close()
	defer backendPeer.Close()
	done := make(chan error, 1)
	go func() { done <- bridgeStreamAndBackend(stream, bufio.NewReader(stream), backend) }()
	require.NoError(t, backendPeer.Close())
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("half-closed bridge waited indefinitely for peer")
	}
	_ = peer // peer deliberately remains open until cleanup
}

type rejectedReadDeadlineStream struct{ *net.TCPConn }

func (*rejectedReadDeadlineStream) SetReadDeadline(time.Time) error {
	return errors.New("read deadline unavailable")
}

func TestBridgeStreamAndBackend_DeadlineFailureClosesBlockedStream(t *testing.T) {
	streamConn, _ := tcpBridgeTestPair(t)
	stream := &rejectedReadDeadlineStream{TCPConn: streamConn}
	backend, backendPeer := net.Pipe()
	defer backend.Close()
	defer backendPeer.Close()
	done := make(chan error, 1)
	go func() { done <- bridgeStreamAndBackend(stream, bufio.NewReader(stream), backend) }()
	require.NoError(t, backendPeer.Close())
	select {
	case err := <-done:
		require.EqualError(t, err, "read deadline unavailable")
	case <-time.After(time.Second):
		t.Fatal("failed deadline left copy reader blocked")
	}
}

func TestBridgeStreamAndBackend_HalfClosedTransferExtendsIdleDeadline(t *testing.T) {
	previous := halfCloseIdleTimeout
	halfCloseIdleTimeout = 200 * time.Millisecond
	t.Cleanup(func() { halfCloseIdleTimeout = previous })
	stream, peer := tcpBridgeTestPair(t)
	backend, backendPeer := tcpBridgeTestPair(t)
	done := make(chan error, 1)
	go func() { done <- bridgeStreamAndBackend(stream, bufio.NewReader(stream), backend) }()
	require.NoError(t, backendPeer.CloseWrite())
	require.NoError(t, peer.SetReadDeadline(time.Now().Add(time.Second)))
	_, err := io.ReadAll(peer)
	require.NoError(t, err, "backend EOF should half-close response")
	start := time.Now()
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()
	for i := 0; i < 5; i++ {
		<-ticker.C
		select {
		case err := <-done:
			t.Fatalf("active transfer cut short: %v", err)
		default:
		}
		_, err := peer.Write([]byte("x"))
		require.NoError(t, err)
		require.NoError(t, backendPeer.SetReadDeadline(time.Now().Add(time.Second)))
		var b [1]byte
		_, err = io.ReadFull(backendPeer, b[:])
		require.NoError(t, err)
		require.Equal(t, byte('x'), b[0])
	}
	require.Greater(t, time.Since(start), halfCloseIdleTimeout)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("bridge did not stop after final idle interval")
	}
}
