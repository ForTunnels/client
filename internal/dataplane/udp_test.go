// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/xtaci/smux"

	"github.com/fortunnels/client/internal/config"
	"github.com/fortunnels/client/internal/support"
	"github.com/fortunnels/client/shared/wsconn"
)

func TestStartDataPlaneUDPContext_WrapFailureIsPermanent(t *testing.T) {
	var websocketConnections atomic.Int32
	upgraded := make(chan struct{}, 2)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		websocketConnections.Add(1)
		upgraded <- struct{}{}
		defer conn.Close()

		session, err := smux.Server(wsconn.NewWSConn(conn), smux.DefaultConfig())
		if err != nil {
			return
		}
		defer session.Close()
		for {
			stream, err := session.AcceptStream()
			if err != nil {
				return
			}
			go func(stream io.ReadWriteCloser) {
				_, _ = io.Copy(io.Discard, stream)
				_ = stream.Close()
			}(stream)
		}
	}))
	defer server.Close()

	wrapFailure := errors.New("nonce source failed")
	originalWrapper := wrapClientStream
	var wrapperCalls atomic.Int32
	wrapClientStream = func(io.ReadWriteCloser, string, config.EncryptionSettings) (io.ReadWriteCloser, error) {
		wrapperCalls.Add(1)
		return nil, wrapFailure
	}
	t.Cleanup(func() { wrapClientStream = originalWrapper })

	runtime := config.RuntimeSettings{
		PingInterval:          30 * time.Second,
		PingTimeout:           5 * time.Second,
		SmuxKeepAliveInterval: 30 * time.Second,
		SmuxKeepAliveTimeout:  90 * time.Second,
	}
	encryption := config.EncryptionSettings{Enabled: true, PSK: "test-secret"}
	listenAddr := "127.0.0.1:0"

	err := StartDataPlaneUDPContext(context.Background(), server.URL, "t1", "127.0.0.1:53", listenAddr, runtime, encryption, "auth")
	var permanent permanentError
	require.ErrorAs(t, err, &permanent)
	require.ErrorIs(t, err, wrapFailure)
	select {
	case <-upgraded:
	case <-time.After(time.Second):
		t.Fatal("UDP startup did not establish its WebSocket session")
	}
	require.EqualValues(t, 1, websocketConnections.Load())

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	strategy := NewStrategy("ws", server.URL, "t1", "auth", "127.0.0.1:53", listenAddr, runtime, encryption, time.Hour, time.Hour)
	err = strategy.RunContext(ctx)
	require.ErrorAs(t, err, &permanent)
	require.ErrorIs(t, err, wrapFailure)
	select {
	case <-upgraded:
	case <-time.After(time.Second):
		t.Fatal("WebSocket strategy did not establish its session")
	}
	require.EqualValues(t, 2, websocketConnections.Load())
	require.EqualValues(t, 2, wrapperCalls.Load())
}

type trackedUDPStream struct {
	net.Conn
	closed atomic.Bool
}

func (s *trackedUDPStream) Close() error {
	s.closed.Store(true)
	return s.Conn.Close()
}

func TestBridgeUDPStreamAndBackend_BackendReadErrorClosesStream(t *testing.T) {
	reserved, err := listenTestUDP()
	require.NoError(t, err)
	addr := reserved.LocalAddr().(*net.UDPAddr)
	require.NoError(t, reserved.Close())
	backend, err := net.DialUDP("udp", nil, addr)
	require.NoError(t, err)
	defer backend.Close()
	streamConn, peer := net.Pipe()
	stream := &trackedUDPStream{Conn: streamConn}
	defer stream.Close()
	defer peer.Close()
	require.NoError(t, peer.SetDeadline(time.Now().Add(2*time.Second)))
	reported := make(chan error, 1)
	done := make(chan error, 1)
	go func() {
		done <- bridgeUDPStreamAndBackend(stream, backend, addr.String(), func(dst string, err error) {
			if dst == addr.String() {
				reported <- err
			}
		})
	}()
	require.NoError(t, writeUDPPacket(peer, []byte("trigger ICMP")))
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("backend read failure left stream blocked")
	}
	require.True(t, stream.closed.Load())
	select {
	case err := <-reported:
		require.Error(t, err)
	default:
		t.Fatal("backend failure was not reported")
	}
}

// mockWriter implements io.Writer for testing
type mockWriter struct {
	data     []byte
	writeErr error
}

func (m *mockWriter) Write(b []byte) (int, error) {
	if m.writeErr != nil {
		return 0, m.writeErr
	}
	m.data = append(m.data, b...)
	return len(b), nil
}

// mockReader implements io.Reader for testing
type mockReader struct {
	data    []byte
	readErr error
}

func (m *mockReader) Read(b []byte) (int, error) {
	if m.readErr != nil {
		return 0, m.readErr
	}
	if len(m.data) == 0 {
		return 0, io.EOF
	}
	n := copy(b, m.data)
	m.data = m.data[n:]
	return n, nil
}

func TestWriteUDPPacket(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
		wantErr bool
	}{
		{
			name:    "small packet",
			payload: []byte("hello"),
			wantErr: false,
		},
		{
			name:    "empty packet",
			payload: []byte{},
			wantErr: false,
		},
		{
			name:    "large packet",
			payload: make([]byte, 65535),
			wantErr: false,
		},
		{
			name:    "max size packet",
			payload: make([]byte, 65507), // Max UDP payload size
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer := &mockWriter{}
			err := writeUDPPacket(writer, tt.payload)
			if (err != nil) != tt.wantErr {
				t.Errorf("writeUDPPacket() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				// Verify frame format: [len(2)|payload]
				if len(writer.data) < 2 {
					t.Error("writeUDPPacket() should write at least 2 bytes (header)")
					return
				}
				// Verify length header
				length := binary.BigEndian.Uint16(writer.data[:2])
				if length != uint16(len(tt.payload)) {
					t.Errorf("writeUDPPacket() length header = %d, want %d", length, len(tt.payload))
				}
				// Verify payload
				if len(writer.data) != 2+len(tt.payload) {
					t.Errorf("writeUDPPacket() total length = %d, want %d", len(writer.data), 2+len(tt.payload))
				}
				if !bytes.Equal(writer.data[2:], tt.payload) {
					t.Error("writeUDPPacket() payload does not match")
				}
			}
		})
	}
}

func TestWriteUDPPacket_WriteError(t *testing.T) {
	writer := &mockWriter{
		writeErr: io.ErrClosedPipe,
	}
	payload := []byte("test")
	err := writeUDPPacket(writer, payload)
	require.Error(t, err, "writeUDPPacket() with write error should return error")
}

func TestReadUDPPacket(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
		wantErr bool
	}{
		{
			name:    "small packet",
			payload: []byte("hello"),
			wantErr: false,
		},
		{
			name:    "empty packet",
			payload: []byte{},
			wantErr: true, // readUDPPacket returns error for n <= 0
		},
		{
			name:    "large packet",
			payload: make([]byte, 1000),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create frame: [len(2)|payload]
			frame := make([]byte, 2+len(tt.payload))
			binary.BigEndian.PutUint16(frame[:2], uint16(len(tt.payload)))
			copy(frame[2:], tt.payload)

			reader := &mockReader{data: frame}
			result, err := readUDPPacket(reader)
			if (err != nil) != tt.wantErr {
				t.Errorf("readUDPPacket() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if !bytes.Equal(result, tt.payload) {
					t.Errorf("readUDPPacket() = %q, want %q", result, tt.payload)
				}
			}
		})
	}
}

func TestReadUDPPacket_InvalidSize(t *testing.T) {
	tests := []struct {
		name          string
		frame         []byte
		wantErr       error
		wantBenignErr bool
	}{
		{
			name:          "zero length",
			frame:         []byte{0, 0},
			wantErr:       errInvalidUDPFrame,
			wantBenignErr: false,
		},
		{
			name:          "oversized UDP payload length",
			frame:         []byte{0xff, 0xff},
			wantErr:       errInvalidUDPFrame,
			wantBenignErr: false,
		},
		{
			name:          "incomplete header",
			frame:         []byte{0},
			wantErr:       io.ErrUnexpectedEOF,
			wantBenignErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &mockReader{data: tt.frame}
			_, err := readUDPPacket(reader)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("readUDPPacket() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got := support.IsBenignCopyError(err); got != tt.wantBenignErr {
				t.Errorf("IsBenignCopyError(%v) = %v, want %v", err, got, tt.wantBenignErr)
			}
		})
	}
}

func TestReadUDPPacket_EOF(t *testing.T) {
	reader := &mockReader{
		readErr: io.EOF,
	}
	_, err := readUDPPacket(reader)
	require.Error(t, err, "readUDPPacket() with EOF should return error")
}

func TestSendUDPPreface(t *testing.T) {
	tests := []struct {
		name     string
		dst      string
		tunnelID string
		wantErr  bool
	}{
		{
			name:     "valid preface",
			dst:      "127.0.0.1:8080",
			tunnelID: "tunnel-123",
			wantErr:  false,
		},
		{
			name:     "empty dst",
			dst:      "",
			tunnelID: "tunnel-123",
			wantErr:  false,
		},
		{
			name:     "empty tunnelID",
			dst:      "127.0.0.1:8080",
			tunnelID: "",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer := &mockWriter{}
			err := sendUDPPreface(writer, tt.dst, tt.tunnelID)
			if (err != nil) != tt.wantErr {
				t.Errorf("sendUDPPreface() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				// Verify JSON preface was written
				if len(writer.data) == 0 {
					t.Error("sendUDPPreface() should write data")
					return
				}
				// Verify it ends with newline
				if writer.data[len(writer.data)-1] != '\n' {
					t.Error("sendUDPPreface() should end with newline")
				}
				// Verify it's valid JSON
				var preface map[string]string
				jsonData := writer.data[:len(writer.data)-1] // Remove newline
				if err := json.Unmarshal(jsonData, &preface); err != nil {
					t.Errorf("sendUDPPreface() wrote invalid JSON: %v", err)
				}
				// Verify fields
				if preface["dst"] != tt.dst {
					t.Errorf("sendUDPPreface() dst = %q, want %q", preface["dst"], tt.dst)
				}
				if preface["proto"] != "udp" {
					t.Errorf("sendUDPPreface() proto = %q, want %q", preface["proto"], "udp")
				}
				if preface["tunnel_id"] != tt.tunnelID {
					t.Errorf("sendUDPPreface() tunnel_id = %q, want %q", preface["tunnel_id"], tt.tunnelID)
				}
			}
		})
	}
}

func TestSendUDPPreface_WriteError(t *testing.T) {
	writer := &mockWriter{
		writeErr: io.ErrClosedPipe,
	}
	err := sendUDPPreface(writer, "127.0.0.1:8080", "tunnel-123")
	require.Error(t, err, "sendUDPPreface() with write error should return error")
}

func startUDPEchoBackend(t *testing.T) (addr string, cleanup func()) {
	t.Helper()
	udpAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	require.NoError(t, err)
	c, err := net.ListenUDP("udp", udpAddr)
	require.NoError(t, err)
	done := make(chan struct{})
	go func() {
		buf := make([]byte, udpMaxPacketSize)
		for {
			select {
			case <-done:
				return
			default:
			}
			_ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
			n, src, readErr := c.ReadFromUDP(buf)
			if readErr != nil {
				continue
			}
			_, _ = c.WriteToUDP(buf[:n], src)
		}
	}()
	return c.LocalAddr().String(), func() {
		close(done)
		_ = c.Close()
	}
}

func TestServeIncomingUDPStream_RoundTrip(t *testing.T) {
	echoAddr, cleanup := startUDPEchoBackend(t)
	defer cleanup()

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()

	errCh := make(chan error, 1)
	go func() {
		errCh <- serveIncomingUDPStream(serverConn, nil)
	}()

	preface := []byte(`{"dst":"` + echoAddr + `","proto":"udp"}` + "\n")
	_, err := clientConn.Write(preface)
	require.NoError(t, err)

	payload := []byte("ping-udp")
	require.NoError(t, writeUDPPacket(clientConn, payload))

	resp, err := readUDPPacket(clientConn)
	require.NoError(t, err)
	require.Equal(t, payload, resp)

	_ = clientConn.Close()
	<-errCh
}

func TestServeIncomingUDPStream_NonUDPPreface(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	go func() {
		_, _ = clientConn.Write([]byte(`{"dst":"127.0.0.1:1","proto":"tcp"}` + "\n"))
		_ = clientConn.Close()
	}()
	err := serveIncomingUDPStream(serverConn, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "expected udp preface")
}
