// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package wsconn

import (
	"errors"
	"io"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
)

// SECURITY: Maximum WebSocket message size to prevent memory exhaustion attacks
const (
	MaxWebSocketMessageSize = 1024 * 1024 // 1MB
	MaxWebSocketFrameSize   = 64 * 1024   // 64KB per frame
)

// WSConn adapts a *websocket.Conn to an io.ReadWriteCloser suitable for smux.
// It reads and writes only binary frames, ignoring non-binary messages.
// SECURITY: Includes message size validation to prevent DoS attacks.
type WSConn struct {
	conn       *websocket.Conn
	readMu     sync.Mutex
	writeMu    sync.Mutex
	currReader io.Reader
}

// NewWSConn constructs a new WSConn adapter for the provided *websocket.Conn.
func NewWSConn(c *websocket.Conn) *WSConn {
	// SECURITY: Set maximum message size limits
	c.SetReadLimit(MaxWebSocketMessageSize)
	return &WSConn{conn: c}
}

// NewClientWSConn mirrors NewWSConn but keeps backwards compatibility.
func NewClientWSConn(c *websocket.Conn) *WSConn { return NewWSConn(c) }

// Read returns data from the current binary message reader, advancing to the
// next binary frame as needed. It skips non-binary frames transparently.
// SECURITY: Validates message size to prevent memory exhaustion.
func (w *WSConn) Read(p []byte) (int, error) {
	w.readMu.Lock()
	defer w.readMu.Unlock()

	// SECURITY: Check if requested buffer size exceeds limits
	if len(p) > MaxWebSocketFrameSize {
		return 0, errors.New("requested buffer size exceeds maximum allowed")
	}

	for {
		if w.currReader == nil {
			// Check if connection is closed before attempting to read
			if w.conn == nil {
				return 0, io.EOF
			}

			mt, r, err := w.conn.NextReader()
			if err != nil {
				// Check for connection closed errors to avoid panic on repeated reads
				if isConnClosed(err) {
					return 0, io.EOF
				}
				return 0, err
			}
			if mt != websocket.BinaryMessage {
				// SECURITY: Limit the amount of data we discard from non-binary messages.
				//nolint:errcheck // best-effort discard of oversized frame
				_, _ = io.CopyN(io.Discard, r, MaxWebSocketFrameSize)
				continue
			}
			w.currReader = r
		}
		n, err := w.currReader.Read(p)
		if err == io.EOF {
			w.currReader = nil
			if n > 0 {
				return n, nil
			}
			// loop to get next frame
			continue
		}
		// Check for connection errors during read
		if err != nil && isConnClosed(err) {
			return n, io.EOF
		}
		return n, err
	}
}

// Write emits a single binary frame containing p. Each call produces a single
// WebSocket binary message.
// SECURITY: Validates message size before sending.
func (w *WSConn) Write(p []byte) (int, error) {
	// SECURITY: Check message size before sending
	if len(p) > MaxWebSocketMessageSize {
		return 0, errors.New("message size exceeds maximum allowed")
	}

	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	writer, err := w.conn.NextWriter(websocket.BinaryMessage)
	if err != nil {
		return 0, err
	}
	n, werr := writer.Write(p)
	cerr := writer.Close()
	if werr != nil {
		return n, werr
	}
	return n, cerr
}

// Close sends a normal close control frame and then closes the underlying socket.
func (w *WSConn) Close() error {
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	closePayload := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
	writeErr := w.conn.WriteMessage(websocket.CloseMessage, closePayload)
	if writeErr != nil && !errors.Is(writeErr, websocket.ErrCloseSent) {
		return writeErr
	}
	return w.conn.Close()
}

func isConnClosed(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "websocket: close") ||
		strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "connection closed") ||
		strings.Contains(msg, "connection reset by peer") ||
		strings.Contains(msg, "broken pipe")
}
