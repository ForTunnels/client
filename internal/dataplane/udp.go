// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"bufio"
	"encoding/binary"
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

// StartDataPlaneServeIncomingUDP accepts smux streams opened by server UDP ingress and bridges to a local UDP backend.
//
//nolint:dupl // TCP and UDP incoming serve loops share the same session accept pattern.
func StartDataPlaneServeIncomingUDP(serverURL, tunnelID string, runtime config.RuntimeSettings, reporter BackendStateReporter, dpAuthToken string) error {
	mgr := NewManager(serverURL, tunnelID, dpAuthToken, time.Second, 30*time.Second, runtime)
	defer mgr.Close()
	for {
		sess, err := mgr.EnsureSession()
		if err != nil {
			return err
		}
		st, err := sess.AcceptStream()
		if err != nil {
			time.Sleep(reconnectRetryDelay)
			continue
		}
		go func(s io.ReadWriteCloser) {
			if serveErr := serveIncomingUDPStream(s, reporter); serveErr != nil && !support.IsBenignCopyError(serveErr) {
				log.Printf("incoming udp stream error: %v", serveErr)
			}
		}(st)
	}
}

func serveIncomingUDPStream(stream io.ReadWriteCloser, reporter BackendStateReporter) error {
	defer stream.Close()
	rd := bufio.NewReader(stream)
	proto, dst, err := readStreamPreface(rd)
	if err != nil {
		return err
	}
	if !strings.EqualFold(proto, "udp") {
		return fmt.Errorf("expected udp preface, got %q", proto)
	}
	if dst == "" {
		return fmt.Errorf("stream preface missing or empty dst")
	}
	raddr, err := net.ResolveUDPAddr("udp", dst)
	if err != nil {
		return fmt.Errorf("resolve udp dst: %w", err)
	}
	uc, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		if reporter != nil {
			reporter(dst, err)
		}
		return err
	}
	defer uc.Close()
	if reporter != nil {
		reporter(dst, nil)
	}
	return bridgeUDPStreamAndBackend(stream, uc)
}

func bridgeUDPStreamAndBackend(stream io.ReadWriteCloser, uc *net.UDPConn) error {
	errCh := make(chan error, 2)
	go func() {
		defer func() { _ = uc.Close() }()
		for {
			packet, readErr := readUDPPacket(stream)
			if readErr != nil {
				errCh <- readErr
				return
			}
			if _, writeErr := uc.Write(packet); writeErr != nil {
				errCh <- writeErr
				return
			}
		}
	}()
	go func() {
		buf := make([]byte, udpMaxPacketSize)
		for {
			n, readErr := uc.Read(buf)
			if readErr != nil {
				errCh <- readErr
				return
			}
			if n <= 0 {
				continue
			}
			if writeErr := writeUDPPacket(stream, buf[:n]); writeErr != nil {
				errCh <- writeErr
				return
			}
		}
	}()
	first := <-errCh
	second := <-errCh
	if first != nil && !support.IsBenignCopyError(first) {
		if second != nil && !support.IsBenignCopyError(second) {
			log.Printf("bridgeUDPStreamAndBackend secondary error: %v", second)
		}
		return first
	}
	if second != nil && !support.IsBenignCopyError(second) {
		return second
	}
	return nil
}

// StartDataPlaneUDP listens on udpListen and forwards via WS/smux to server.
func StartDataPlaneUDP(serverURL, tunnelID, dst, listenAddr string, runtime config.RuntimeSettings, enc config.EncryptionSettings, dpAuthToken string) error {
	sess, cleanup, err := CreateDataPlaneSession(serverURL, tunnelID, runtime, dpAuthToken)
	if err != nil {
		return err
	}
	defer cleanup()
	stream, err := sess.OpenStream()
	if err != nil {
		return fmt.Errorf("open stream: %w", err)
	}
	defer stream.Close()
	if prefaceErr := sendUDPPreface(stream, dst, tunnelID); prefaceErr != nil {
		return prefaceErr
	}
	wrapped := WrapClientStream(stream, tunnelID, enc)
	// local UDP socket
	laddr, err := net.ResolveUDPAddr("udp", listenAddr)
	if err != nil {
		return fmt.Errorf("resolve udp listen: %w", err)
	}
	uc, err := net.ListenUDP("udp", laddr)
	if err != nil {
		return fmt.Errorf("listen udp: %w", err)
	}
	defer uc.Close()

	// stop serving if tunnel was deleted on server (future enhancement via watchTunnelDeleted)
	errCh := make(chan error, 2)
	var lastSrcMu sync.RWMutex
	var lastSrc *net.UDPAddr
	startUDPLocalToStream(wrapped, uc, errCh, &lastSrcMu, &lastSrc)
	startStreamToUDPLocal(wrapped, uc, errCh, &lastSrcMu, &lastSrc)
	return <-errCh
}

func sendUDPPreface(stream io.Writer, dst, tunnelID string) error {
	payload, err := encodePreface(map[string]string{"dst": dst, "proto": "udp", "tunnel_id": tunnelID})
	if err != nil {
		return err
	}
	if _, err := stream.Write(payload); err != nil {
		return fmt.Errorf("write preface: %w", err)
	}
	return nil
}

func startUDPLocalToStream(
	wrapped io.Writer,
	uc *net.UDPConn,
	errCh chan<- error,
	lastSrcMu *sync.RWMutex,
	lastSrc **net.UDPAddr,
) {
	go func() {
		buf := make([]byte, udpMaxPacketSize)
		for {
			n, src, err := uc.ReadFromUDP(buf)
			if err != nil {
				errCh <- err
				return
			}
			if n <= 0 {
				continue
			}

			lastSrcMu.Lock()
			*lastSrc = src
			lastSrcMu.Unlock()

			if writeErr := writeUDPPacket(wrapped, buf[:n]); writeErr != nil {
				errCh <- writeErr
				return
			}
		}
	}()
}

func startStreamToUDPLocal(
	wrapped io.Reader,
	uc *net.UDPConn,
	errCh chan<- error,
	lastSrcMu *sync.RWMutex,
	lastSrc **net.UDPAddr,
) {
	go func() {
		for {
			packet, err := readUDPPacket(wrapped)
			if err != nil {
				errCh <- err
				return
			}
			lastSrcMu.RLock()
			dst := *lastSrc
			lastSrcMu.RUnlock()
			if dst == nil {
				continue
			}
			if _, writeErr := uc.WriteToUDP(packet, dst); writeErr != nil {
				errCh <- writeErr
				return
			}
		}
	}()
}

func writeUDPPacket(w io.Writer, payload []byte) error {
	length, err := support.ToUint16Size(len(payload))
	if err != nil {
		return err
	}
	var hdr [2]byte
	binary.BigEndian.PutUint16(hdr[:], length)
	if _, writeErr := w.Write(hdr[:]); writeErr != nil {
		return writeErr
	}
	_, writeErr := w.Write(payload)
	return writeErr
}

func readUDPPacket(r io.Reader) ([]byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(hdr[:]))
	if n <= 0 || n > udpMaxPacketSize {
		return nil, io.ErrUnexpectedEOF
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}
