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

// startDataPlane establishes a single smux stream over a WS for echo test.
func StartDataPlane(serverURL, tunnelID, dst string, runtime config.RuntimeSettings, enc config.EncryptionSettings) error {
	client, err := NewWSSmuxClient(serverURL, tunnelID, runtime)
	if err != nil {
		return err
	}
	defer client.Close()

	stream, err := client.Session().OpenStream()
	if err != nil {
		return fmt.Errorf("open stream: %w", err)
	}
	defer stream.Close()

	// send preface json + \n
	b, err := encodePreface(map[string]string{"dst": dst, "proto": "tcp"})
	if err != nil {
		return err
	}
	if _, writeErr := stream.Write(b); writeErr != nil {
		return fmt.Errorf("write preface: %w", writeErr)
	}

	wrapped := WrapClientStream(stream, tunnelID, enc)

	// send a small message and read echo
	msg := []byte("hello over smux tcp\n")
	if _, writeErr := wrapped.Write(msg); writeErr != nil {
		return fmt.Errorf("write payload: %w", writeErr)
	}

	buf := make([]byte, tcpEchoBufferSize)
	if err := client.Conn().SetReadDeadline(time.Now().Add(tcpEchoTimeout)); err != nil {
		log.Printf("set read deadline: %v", err)
	}
	n, readErr := wrapped.Read(buf)
	if readErr != nil {
		return fmt.Errorf("read echo: %w", readErr)
	}
	fmt.Printf("🔁 Echo: %s\n", string(buf[:n]))
	return nil
}

// startDataPlaneParallel opens n streams concurrently and verifies echoes.
func StartDataPlaneParallel(serverURL, tunnelID, dst string, n int, runtime config.RuntimeSettings, enc config.EncryptionSettings) error {
	client, err := NewWSSmuxClient(serverURL, tunnelID, runtime)
	if err != nil {
		return err
	}
	defer client.Close()

	var wg sync.WaitGroup
	wg.Add(n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			stream, err := client.Session().OpenStream()
			if err != nil {
				errs[i] = fmt.Errorf("open stream: %w", err)
				return
			}
			defer stream.Close()

			b, err := encodePreface(map[string]string{"dst": dst, "proto": "tcp"})
			if err != nil {
				errs[i] = err
				return
			}
			if _, writeErr := stream.Write(b); writeErr != nil {
				errs[i] = fmt.Errorf("write preface: %w", writeErr)
				return
			}
			wrapped := WrapClientStream(stream, tunnelID, enc)
			msg := []byte(fmt.Sprintf("hello stream %d\n", i))
			if _, writeErr := wrapped.Write(msg); writeErr != nil {
				errs[i] = fmt.Errorf("write payload: %w", writeErr)
				return
			}
			buf := make([]byte, tcpEchoBufferSize)
			if err := client.Conn().SetReadDeadline(time.Now().Add(tcpEchoTimeout)); err != nil {
				log.Printf("set read deadline: %v", err)
			}
			n, readErr := wrapped.Read(buf)
			if readErr != nil {
				errs[i] = fmt.Errorf("read echo: %w", readErr)
				return
			}
			fmt.Printf("🔁 Echo[%d]: %s\n", i, string(buf[:n]))
		}()
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

func StartDataPlaneServeListenReconnect(
	serverURL, tunnelID, dst, listenAddr string,
	boInit, boMax time.Duration,
	runtime config.RuntimeSettings,
	enc config.EncryptionSettings,
) error {
	mgr := NewManager(serverURL, tunnelID, boInit, boMax, runtime)
	defer mgr.Close()

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer SafeClose(ln)

	// stop serving if tunnel was deleted on server
	// go func() { <-watchTunnelDeleted(serverURL, tunnelID, 3*time.Second); mgr.close(); _ = ln.Close() }()

	for {
		lconn, err := ln.Accept()
		if err != nil {
			return fmt.Errorf("accept: %w", err)
		}
		go func(c net.Conn) {
			defer SafeClose(c)
			// ensure session
			sess, err := mgr.EnsureSession()
			if err != nil {
				log.Printf("ensure session: %v", err)
				return
			}
			// open stream
			stream, err := sess.OpenStream()
			if err != nil {
				log.Printf("open stream: %v", err)
				return
			}
			defer SafeClose(stream)
			b, err := encodePreface(map[string]string{"dst": dst, "proto": "tcp"})
			if err != nil {
				log.Printf("marshal preface: %v", err)
				return
			}
			if _, err := stream.Write(b); err != nil {
				log.Printf("write preface: %v", err)
				return
			}
			wrapped := WrapClientStream(stream, tunnelID, enc)
			PipeStreams(c, wrapped)
		}(lconn)
	}
}

// BackendStateReporter is called on backend dial success/failure for CLI transition messages.
// If nil, no reporting is done.
type BackendStateReporter func(dst string, err error)

// NewBackendStateReporter returns a reporter that prints one-time messages on backend down/up transitions.
func NewBackendStateReporter() BackendStateReporter {
	var mu sync.Mutex
	state := make(map[string]bool) // dst -> wasDown
	return func(dst string, err error) {
		mu.Lock()
		defer mu.Unlock()
		wasDown := state[dst]
		if err != nil {
			if !wasDown {
				fmt.Printf("⚠️  Backend unreachable for %s\n", dst)
			}
			state[dst] = true
		} else {
			if wasDown {
				fmt.Printf("✅ Backend recovered for %s\n", dst)
			}
			state[dst] = false
		}
	}
}

func StartDataPlaneServeIncoming(serverURL, tunnelID string, runtime config.RuntimeSettings, reporter BackendStateReporter) error {
	mgr := NewManager(serverURL, tunnelID, time.Second, 30*time.Second, runtime)
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
		_, _ = stream.Write(append(b, '\n'))
	}
}

func serveIncomingStream(stream io.ReadWriteCloser, reporter BackendStateReporter) error {
	defer stream.Close()
	rd := bufio.NewReader(stream)
	dst, err := readStreamDestination(rd)
	if err != nil || dst == "" {
		return err
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
		_ = cw.CloseWrite()
	}
}

func closeWriteOrClose(stream io.ReadWriteCloser) {
	type closeWriter interface{ CloseWrite() error }
	if cw, ok := stream.(closeWriter); ok {
		_ = cw.CloseWrite()
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
