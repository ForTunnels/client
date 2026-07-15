// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package wsconn

import (
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestWSConnReadRejectsLargeBuffer(t *testing.T) {
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
	buf := make([]byte, MaxWebSocketFrameSize+1)
	_, err = wsc.Read(buf)
	require.Error(t, err, "Read() expected error for oversized buffer")
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
