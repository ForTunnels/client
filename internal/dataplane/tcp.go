// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/fortunnels/client/internal/config"
	"github.com/fortunnels/client/internal/support"
)

// BackendStateReporter is called on backend dial success/failure for CLI transition messages.
// Success means TCP dial reached the target; it does not imply HTTP/TLS readiness.
// If nil, no reporting is done.
type BackendStateReporter func(dst string, err error)

// NewBackendStateReporter returns a reporter that prints one-time messages on backend dial
// down/up transitions. Messages reflect transport-level reachability only, not full proxy readiness.
func NewBackendStateReporter() BackendStateReporter {
	var mu sync.Mutex
	state := make(map[string]bool) // dst -> wasDown
	return func(dst string, err error) {
		mu.Lock()
		defer mu.Unlock()
		wasDown := state[dst]
		if err != nil {
			if !wasDown {
				fmt.Printf("⚠️  Backend unreachable for %s — start your backend\n", dst)
			}
			state[dst] = true
		} else {
			if wasDown {
				fmt.Printf("✅ Backend reachable for %s\n", dst)
			}
			state[dst] = false
		}
	}
}

func StartDataPlaneServeIncoming(serverURL, tunnelID string, runtime config.RuntimeSettings, reporter BackendStateReporter, dpAuthToken string) error {
	mgr := NewManager(serverURL, tunnelID, dpAuthToken, time.Second, 30*time.Second, runtime)
	defer mgr.Close()
	for {
		// ensure session alive
		sess, err := mgr.EnsureSession()
		if err != nil {
			return err
		}
		st, err := sess.AcceptStream()
		if err != nil {
			// session likely closed; retry loop will recreate
			time.Sleep(reconnectRetryDelay)
			continue
		}
		go func(s io.ReadWriteCloser) {
			if err := serveIncomingStream(s, reporter); err != nil && !support.IsBenignCopyError(err) {
				log.Printf("incoming stream error: %v", err)
			}
		}(st)
	}
}

// setupAck and setupError are JSON lines sent to the server for proxy error classification.
const setupAckLine = `{"ok":true}` + "\n"

func writeSetupError(stream io.Writer, err error) {
	payload := map[string]interface{}{"ok": false, "error": err.Error()}
	if b, e := json.Marshal(payload); e == nil {
		if _, wErr := stream.Write(append(b, '\n')); wErr != nil {
			log.Printf("writeSetupError: %v", wErr)
		}
	}
}

func serveIncomingStream(stream io.ReadWriteCloser, reporter BackendStateReporter) error {
	defer stream.Close()
	rd := bufio.NewReader(stream)
	dst, err := readStreamDestination(rd)
	if err != nil {
		return err
	}
	if dst == "" {
		return fmt.Errorf("stream preface missing or empty dst")
	}
	bc, err := net.Dial("tcp", dst)
	if err != nil {
		if reporter != nil {
			reporter(dst, err)
		}
		writeSetupError(stream, err)
		return err
	}
	defer bc.Close()

	if reporter != nil {
		reporter(dst, nil)
	}
	if _, err := stream.Write([]byte(setupAckLine)); err != nil {
		return err
	}

	if err := flushBufferedBytes(rd, bc); err != nil {
		return err
	}
	return bridgeStreamAndBackend(stream, rd, bc)
}

func bridgeStreamAndBackend(stream io.ReadWriteCloser, streamReader io.Reader, backendConn net.Conn) error {
	errCh := make(chan error, 2)

	go func() {
		_, err := io.Copy(stream, backendConn)
		// Propagate response EOF to the server-side proxy. Without this, HTTP/1.0
		// responses without Content-Length can hang until client timeout.
		closeWriteOrClose(stream)
		if err != nil && !support.IsBenignCopyError(err) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	go func() {
		_, err := io.Copy(backendConn, streamReader)
		closeWriteIfPossible(backendConn)
		if err != nil && !support.IsBenignCopyError(err) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	first := <-errCh
	second := <-errCh
	if first != nil {
		return first
	}
	return second
}

func closeWriteIfPossible(c interface{}) {
	type closeWriter interface{ CloseWrite() error }
	if cw, ok := c.(closeWriter); ok {
		if err := cw.CloseWrite(); err != nil {
			log.Printf("closeWriteIfPossible: %v", err)
		}
	}
}

func closeWriteOrClose(stream io.ReadWriteCloser) {
	type closeWriter interface{ CloseWrite() error }
	if cw, ok := stream.(closeWriter); ok {
		if err := cw.CloseWrite(); err != nil {
			log.Printf("closeWriteOrClose: %v", err)
		}
		return
	}
	_ = stream.Close()
}

// flushBufferedBytes forwards only bytes already buffered in rd without blocking.
// This prevents a deadlock where io.Copy waits for stream close before bridge startup.
func flushBufferedBytes(rd *bufio.Reader, dst io.Writer) error {
	buffered := rd.Buffered()
	if buffered == 0 {
		return nil
	}
	buf := make([]byte, buffered)
	if _, err := io.ReadFull(rd, buf); err != nil {
		return err
	}
	for len(buf) > 0 {
		n, err := dst.Write(buf)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		buf = buf[n:]
	}
	return nil
}

func readStreamDestination(rd *bufio.Reader) (string, error) {
	for {
		line, err := rd.ReadString('\n')
		if err != nil {
			return "", err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var pre map[string]string
		if err := json.Unmarshal([]byte(line), &pre); err != nil {
			return "", err
		}
		return pre["dst"], nil
	}
}
