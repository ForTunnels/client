// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"

	"github.com/fortunnels/client/internal/config"
	"github.com/fortunnels/client/internal/support"
	protocolv1 "github.com/fortunnels/client/shared/protocol/v1"
)

var errInvalidUDPFrame = errors.New("invalid UDP frame")

// StartDataPlaneServeIncomingUDP accepts smux streams opened by server UDP ingress and bridges to a local UDP backend.
func StartDataPlaneServeIncomingUDPContext(
	ctx context.Context,
	serverURL, tunnelID string,
	runtime config.RuntimeSettings,
	reporter BackendStateReporter,
	dpAuthToken string,
	onLifecycle func(protocolv1.LifecycleEventPayload),
) error {
	return startIncomingDataPlaneContext(
		ctx, serverURL, tunnelID, runtime, reporter, dpAuthToken, onLifecycle, serveIncomingUDPStream, "incoming udp stream error",
	)
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
	return bridgeUDPStreamAndBackend(stream, uc, dst, reporter)
}

func bridgeUDPStreamAndBackend(stream io.ReadWriteCloser, uc *net.UDPConn, dst string, reporter BackendStateReporter) error {
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
		defer stream.Close()
		buf := make([]byte, udpMaxPacketSize)
		for {
			n, readErr := uc.Read(buf)
			if readErr != nil {
				if reporter != nil && !support.IsBenignCopyError(readErr) {
					reporter(dst, readErr)
				}
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
func StartDataPlaneUDPContext(ctx context.Context, serverURL, tunnelID, dst, listenAddr string, runtime config.RuntimeSettings, enc config.EncryptionSettings, dpAuthToken string) error {
	if _, _, _, err := buildWebSocketURL(serverURL, tunnelID, dpAuthToken); err != nil {
		return permanentError{err}
	}
	laddr, err := net.ResolveUDPAddr("udp", listenAddr)
	if err != nil {
		return permanentError{fmt.Errorf("resolve udp listen: %w", err)}
	}
	uc, err := net.ListenUDP("udp", laddr)
	if err != nil {
		return permanentError{fmt.Errorf("listen udp: %w", err)}
	}
	defer uc.Close()
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
	wrapped, err := wrapClientStream(stream, tunnelID, enc)
	if err != nil {
		return permanentError{fmt.Errorf("wrap UDP stream: %w", err)}
	}
	stopCloser := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = uc.Close()
			cleanup()
		case <-stopCloser:
		}
	}()
	defer close(stopCloser)

	// stop serving if tunnel was deleted on server (future enhancement via watchTunnelDeleted)
	errCh := make(chan error, 2)
	var lastSrcMu sync.RWMutex
	var lastSrc *net.UDPAddr
	startUDPLocalToStream(wrapped, uc, errCh, &lastSrcMu, &lastSrc)
	startStreamToUDPLocal(wrapped, uc, errCh, &lastSrcMu, &lastSrc)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
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
	startUDPLocalToPackets(func(payload []byte) error { return writeUDPPacket(wrapped, payload) }, 0,
		uc, errCh, lastSrcMu, lastSrc)
}

type (
	packetWriter func([]byte) error
	packetReader func() ([]byte, error)
)

func startUDPLocalToPackets(
	writePacket packetWriter,
	maxPayload int,
	uc *net.UDPConn,
	errCh chan<- error,
	lastSrcMu *sync.RWMutex,
	lastSrc **net.UDPAddr,
) {
	go func() {
		buf := make([]byte, udpMaxPacketSize)
		var drops uint64
		for {
			n, src, err := uc.ReadFromUDP(buf)
			if err != nil {
				errCh <- err
				return
			}
			if n <= 0 {
				continue
			}
			if maxPayload > 0 && n > maxPayload {
				logDTLSRecordDrop(&drops, n)
				continue
			}

			lastSrcMu.Lock()
			*lastSrc = src
			lastSrcMu.Unlock()

			if writeErr := writePacket(buf[:n]); writeErr != nil {
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
	startPacketsToUDPLocal(func() ([]byte, error) { return readUDPPacket(wrapped) }, uc, errCh, lastSrcMu, lastSrc)
}

func startPacketsToUDPLocal(
	readPacket packetReader,
	uc *net.UDPConn,
	errCh chan<- error,
	lastSrcMu *sync.RWMutex,
	lastSrc **net.UDPAddr,
) {
	go func() {
		var drops uint64
		for {
			packet, err := readPacket()
			if errors.Is(err, errInvalidDTLSRecord) || errors.Is(err, errDTLSRecordPayloadTooLarge) {
				logDTLSRecordDrop(&drops, len(packet))
				continue
			}
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
	if n <= 0 || n > udpDatagramMaxSize {
		return nil, fmt.Errorf("%w: payload length %d", errInvalidUDPFrame, n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}
