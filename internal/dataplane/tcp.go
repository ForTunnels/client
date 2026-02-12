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

func StartDataPlaneServeIncoming(serverURL, tunnelID string, runtime config.RuntimeSettings) error {
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
			if err := serveIncomingStream(s); err != nil && !support.IsBenignCopyError(err) {
				log.Printf("incoming stream error: %v", err)
			}
		}(st)
	}
}

func serveIncomingStream(stream io.ReadWriteCloser) error {
	defer stream.Close()
	rd := bufio.NewReader(stream)
	dst, err := readStreamDestination(rd)
	if err != nil || dst == "" {
		return err
	}
	bc, err := net.Dial("tcp", dst)
	if err != nil {
		return err
	}
	defer bc.Close()

	if rd.Buffered() > 0 {
		if _, err := io.Copy(bc, rd); err != nil {
			return err
		}
	}

	go func() {
		if _, err := io.Copy(stream, bc); err != nil && !support.IsBenignCopyError(err) {
			log.Printf("Error copying from backend to stream: %v", err)
		}
	}()
	if _, err := io.Copy(bc, stream); err != nil && !support.IsBenignCopyError(err) {
		return err
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
