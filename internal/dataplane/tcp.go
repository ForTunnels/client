// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fortunnels/client/internal/config"
	"github.com/fortunnels/client/internal/support"
	protocolv1 "github.com/fortunnels/client/shared/protocol/v1"
)

// BackendStateReporter is called on backend dial success/failure for CLI transition messages.
// Success means TCP dial reached the target; it does not imply HTTP/TLS readiness.
// If nil, no reporting is done.
type BackendStateReporter func(dst string, err error)

// NewBackendStateReporter returns a reporter that prints one-time messages on backend dial
// down/up transitions for the requested protocol. Messages reflect transport-level
// reachability only, not full proxy readiness.
func NewBackendStateReporter(protocol string) BackendStateReporter {
	return newBackendStateReporter(protocol, os.Stdout)
}

func newBackendStateReporter(protocol string, output io.Writer) BackendStateReporter {
	if output == nil {
		output = io.Discard
	}
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	var mu sync.Mutex
	state := make(map[string]bool) // dst -> wasDown
	return func(dst string, err error) {
		mu.Lock()
		defer mu.Unlock()
		wasDown := state[dst]
		if err != nil {
			if !wasDown {
				fmt.Fprintf(
					output,
					"⚠️  Backend unreachable for %s (%s) — tunnel remains active; %s. Start the local backend on this target.\n",
					dst,
					backendProtocolLabel(protocol),
					backendRetryGuidance(protocol),
				)
			}
			state[dst] = true
		} else {
			if wasDown {
				fmt.Fprintf(output, "✅ Backend reachable for %s (%s); forwarding new traffic.\n", dst, backendProtocolLabel(protocol))
			}
			state[dst] = false
		}
	}
}

func backendProtocolLabel(protocol string) string {
	if protocol == "" {
		return "TCP"
	}
	return strings.ToUpper(protocol)
}

func backendRetryGuidance(protocol string) string {
	switch protocol {
	case "udp":
		return "the next incoming datagram retries this UDP backend"
	case "tcp":
		return "the next incoming connection retries this TCP backend"
	case "https":
		return "the next incoming HTTPS request retries this backend"
	default:
		return "the next incoming HTTP request retries this backend"
	}
}

func StartDataPlaneServeIncoming(serverURL, tunnelID string, runtime config.RuntimeSettings, reporter BackendStateReporter, dpAuthToken string) error {
	return StartDataPlaneServeIncomingContext(context.Background(), serverURL, tunnelID, runtime, reporter, dpAuthToken, nil)
}

func StartDataPlaneServeIncomingContext(
	ctx context.Context,
	serverURL, tunnelID string,
	runtime config.RuntimeSettings,
	reporter BackendStateReporter,
	dpAuthToken string,
	onLifecycle func(protocolv1.LifecycleEventPayload),
) error {
	return startIncomingDataPlaneContext(
		ctx, serverURL, tunnelID, runtime, reporter, dpAuthToken, onLifecycle, serveIncomingStream, "incoming stream error",
	)
}

type incomingStreamHandler func(io.ReadWriteCloser, BackendStateReporter) error

func startIncomingDataPlaneContext(
	ctx context.Context,
	serverURL, tunnelID string,
	runtime config.RuntimeSettings,
	reporter BackendStateReporter,
	dpAuthToken string,
	onLifecycle func(protocolv1.LifecycleEventPayload),
	handleStream incomingStreamHandler,
	errorLabel string,
) error {
	mgr := NewManager(serverURL, tunnelID, dpAuthToken, time.Second, 30*time.Second, runtime)
	mgr.SetLifecycleHandler(onLifecycle)
	defer mgr.Close()
	stopCloser := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			mgr.Close()
		case <-stopCloser:
		}
	}()
	defer close(stopCloser)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		sess, err := mgr.EnsureSessionContext(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		st, err := sess.AcceptStream()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			mgr.resetSession()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(reconnectRetryDelay):
			}
			continue
		}
		go func(s io.ReadWriteCloser) {
			if err := handleStream(s, reporter); err != nil && !support.IsBenignCopyError(err) {
				log.Printf("%s: %v", errorLabel, err)
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
		if second != nil && !support.IsBenignCopyError(second) {
			log.Printf("bridgeStreamAndBackend secondary error: %v", second)
		}
		return first
	}
	if second != nil && !support.IsBenignCopyError(second) {
		log.Printf("bridgeStreamAndBackend secondary error: %v", second)
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
// The caller must not read from rd concurrently while this runs.
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
	_, dst, err := readStreamPreface(rd)
	return dst, err
}

func readStreamPreface(rd *bufio.Reader) (proto, dst string, err error) {
	for {
		line, readErr := rd.ReadString('\n')
		if readErr != nil {
			return "", "", readErr
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var pre map[string]string
		if unmarshalErr := json.Unmarshal([]byte(line), &pre); unmarshalErr != nil {
			return "", "", unmarshalErr
		}
		return pre["proto"], pre["dst"], nil
	}
}
