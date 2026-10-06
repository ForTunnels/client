// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package wsconn

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestWSConnReadSkipsNonBinaryFrames(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteMessage(websocket.TextMessage, []byte("ignore"))
		_ = conn.WriteMessage(websocket.BinaryMessage, []byte("ok"))
		time.Sleep(50 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err, "dial ws")
	defer conn.Close()

	wsc := NewWSConn(conn)
	buf := make([]byte, MaxWebSocketFrameSize)
	n, err := wsc.Read(buf)
	require.NoError(t, err, "Read()")
	require.Equal(t, "ok", string(buf[:n]), "Read()")
}

func TestWSConnReadClampsLargeBuffer(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteMessage(websocket.BinaryMessage, bytes.Repeat([]byte("x"), 70*1024))
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err, "dial ws")
	defer conn.Close()

	wsc := NewWSConn(conn)
	buf := make([]byte, 70*1024)
	n, err := wsc.Read(buf)
	require.NoError(t, err)
	require.Positive(t, n)
	require.LessOrEqual(t, n, MaxWebSocketFrameSize)
	require.Equal(t, bytes.Repeat([]byte("x"), n), buf[:n])
}

type stalledTransport struct {
	started  chan struct{}
	closed   chan struct{}
	once     sync.Once
	deadline time.Time
}

func (*stalledTransport) SetReadLimit(int64)                       {}
func (*stalledTransport) NextReader() (int, io.Reader, error)      { return 0, nil, io.EOF }
func (s *stalledTransport) NextWriter(int) (io.WriteCloser, error) { return stalledWriter{s}, nil }
func (*stalledTransport) WriteMessage(int, []byte) error           { return websocket.ErrCloseSent }
func (s *stalledTransport) WriteControl(messageType int, payload []byte, deadline time.Time) error {
	s.deadline = deadline
	return websocket.ErrCloseSent
}

func (s *stalledTransport) Close() error {
	s.once.Do(func() { close(s.closed) })
	return nil
}

type stalledWriter struct{ transport *stalledTransport }

func (s stalledWriter) Write([]byte) (int, error) {
	close(s.transport.started)
	<-s.transport.closed
	return 0, io.ErrClosedPipe
}
func (stalledWriter) Close() error { return nil }

func TestWSConnClose_ReturnsWhileWriteStalled(t *testing.T) {
	transport := &stalledTransport{started: make(chan struct{}), closed: make(chan struct{})}
	defer transport.Close()
	conn := NewWSConn(transport)
	written := make(chan struct{})
	go func() { _, _ = conn.Write([]byte("data")); close(written) }()
	select {
	case <-transport.started:
	case <-time.After(time.Second):
		t.Fatal("writer did not stall")
	}
	beforeClose := time.Now()
	finished := make(chan error, 1)
	go func() { finished <- conn.Close() }()
	select {
	case err := <-finished:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Close waited for stalled data writer")
	}
	require.WithinDuration(t, beforeClose.Add(time.Second), transport.deadline, 100*time.Millisecond)
	select {
	case <-transport.closed:
	default:
		t.Fatal("underlying socket was not closed")
	}
	select {
	case <-written:
	case <-time.After(time.Second):
		t.Fatal("socket closure did not unblock data writer")
	}
}

func TestWSConnWriteRejectsLargeMessage(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		time.Sleep(50 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err, "dial ws")
	defer conn.Close()

	wsc := NewWSConn(conn)
	msg := make([]byte, MaxWebSocketMessageSize+1)
	_, err = wsc.Write(msg)
	require.Error(t, err, "Write() expected error for oversized message")
}
