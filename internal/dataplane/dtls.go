// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/url"
	"sync"

	dtls "github.com/pion/dtls/v3"
)

var resolveDTLSServerAddr = net.ResolveUDPAddr

const maxDTLSRecordPayload = 8137

var (
	errDTLSRecordPayloadTooLarge = errors.New("DTLS record payload too large")
	errInvalidDTLSRecord         = errors.New("invalid DTLS record")
)

// startDTLSDataPlaneUDP listens on udpListen and forwards via DTLS to server
func startDTLSDataPlaneUDPContext(
	ctx context.Context,
	serverURL, dtlsPort, tunnelID, authToken, udpDst, udpListen, transportCAPath string,
) error {
	// local UDP listen
	laddr, err := net.ResolveUDPAddr("udp", udpListen)
	if err != nil {
		return permanentError{err}
	}
	uc, err := net.ListenUDP("udp", laddr)
	if err != nil {
		return permanentError{err}
	}
	defer uc.Close()
	// resolve server host and dtls port (from default config 4444)
	u, err := url.Parse(serverURL)
	if err != nil {
		return permanentError{err}
	}
	if (u.Scheme != schemeHTTP && u.Scheme != schemeHTTPS) || u.Hostname() == "" {
		return permanentError{fmt.Errorf("invalid server URL")}
	}
	if _, portErr := net.LookupPort("udp", dtlsPort); portErr != nil {
		return permanentError{fmt.Errorf("invalid DTLS port: %w", portErr)}
	}
	host := net.JoinHostPort(u.Hostname(), dtlsPort)
	options := []dtls.ClientOption{
		dtls.WithExtendedMasterSecret(dtls.RequireExtendedMasterSecret),
		dtls.WithServerName(u.Hostname()),
	}
	roots, err := loadTransportRootCAs(transportCAPath)
	if err != nil {
		return permanentError{err}
	}
	if roots != nil {
		options = append(options, dtls.WithRootCAs(roots))
	}
	// Remote hostname lookup is part of the connection attempt. DNS outages
	// must enter the retry loop rather than terminate the tunnel command.
	uaddr, err := resolveDTLSServerAddr("udp", host)
	if err != nil {
		return err
	}
	conn, err := dtls.DialWithOptions("udp", uaddr, options...)
	if err != nil {
		return classifyTransportDialError(err)
	}
	var closeOnce sync.Once
	closeResources := func() {
		closeOnce.Do(func() {
			_ = conn.Close()
			_ = uc.Close()
		})
	}
	defer closeResources()
	stopCloser := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			closeResources()
		case <-stopCloser:
		}
	}()
	defer close(stopCloser)
	// bootstrap with destination
	b, err := encodePreface(map[string]string{"auth": authToken, "tunnel_id": tunnelID, "dst": udpDst, "framing": "record"})
	if err != nil {
		return err
	}
	// The handshake may complete lazily on the first write, so certificate
	// failures can surface here rather than from the dial.
	if _, err := conn.Write(b); err != nil {
		return classifyTransportDialError(err)
	}
	var lastSrcMu sync.RWMutex
	var lastSrc *net.UDPAddr
	errCh := make(chan error, 2)
	startUDPLocalToPackets(func(payload []byte) error { return writeUDPDatagramRecord(conn, payload) },
		maxDTLSRecordPayload, uc, errCh, &lastSrcMu, &lastSrc)
	recordBuf := make([]byte, udpMaxPacketSize)
	startPacketsToUDPLocal(func() ([]byte, error) { return readUDPDatagramRecord(conn, recordBuf) },
		uc, errCh, &lastSrcMu, &lastSrc)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

func writeUDPDatagramRecord(w io.Writer, payload []byte) error {
	size := len(payload)
	if size > maxDTLSRecordPayload || size > math.MaxUint16 {
		return fmt.Errorf("%w: %d > %d", errDTLSRecordPayloadTooLarge, size, maxDTLSRecordPayload)
	}
	frame := make([]byte, 2+size)
	binary.BigEndian.PutUint16(frame[:2], uint16(size))
	copy(frame[2:], payload)
	n, err := w.Write(frame)
	if err != nil {
		return err
	}
	if n != len(frame) {
		return io.ErrShortWrite
	}
	return nil
}

func readUDPDatagramRecord(r io.Reader, buf []byte) ([]byte, error) {
	n, err := r.Read(buf)
	if err != nil {
		return nil, err
	}
	if n < 2 || int(binary.BigEndian.Uint16(buf[:2])) != n-2 {
		return buf[:n], fmt.Errorf("%w: size %d", errInvalidDTLSRecord, n)
	}
	payload := buf[2:n]
	if len(payload) > maxDTLSRecordPayload {
		return payload, fmt.Errorf("%w: %d > %d", errDTLSRecordPayloadTooLarge, len(payload), maxDTLSRecordPayload)
	}
	return payload, nil
}

func logDTLSRecordDrop(drops *uint64, size int) {
	*drops++
	if *drops == 1 || *drops%100 == 0 {
		log.Printf("dtls: dropped record payload (size=%d limit=%d drops=%d)", size, maxDTLSRecordPayload, *drops)
	}
}
