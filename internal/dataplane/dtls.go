// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"bufio"
	"context"
	"net"
	"net/url"
	"sync"

	dtls "github.com/pion/dtls/v3"
)

// startDTLSDataPlaneUDP listens on udpListen and forwards via DTLS to server
func StartDTLSDataPlaneUDP(serverURL, dtlsPort, tunnelID, authToken, udpDst, udpListen string) error {
	return StartDTLSDataPlaneUDPContext(context.Background(), serverURL, dtlsPort, tunnelID, authToken, udpDst, udpListen)
}

func StartDTLSDataPlaneUDPContext(ctx context.Context, serverURL, dtlsPort, tunnelID, authToken, udpDst, udpListen string) error {
	return startDTLSDataPlaneUDPContext(ctx, serverURL, dtlsPort, tunnelID, authToken, udpDst, udpListen, "")
}

func startDTLSDataPlaneUDPContext(
	ctx context.Context,
	serverURL, dtlsPort, tunnelID, authToken, udpDst, udpListen, transportCAPath string,
) error {
	// local UDP listen
	laddr, err := net.ResolveUDPAddr("udp", udpListen)
	if err != nil {
		return err
	}
	uc, err := net.ListenUDP("udp", laddr)
	if err != nil {
		return err
	}
	// resolve server host and dtls port (from default config 4444)
	u, err := url.Parse(serverURL)
	if err != nil {
		return err
	}
	host := net.JoinHostPort(u.Hostname(), dtlsPort)
	// DTLS dial with proper certificate validation
	uaddr, err := net.ResolveUDPAddr("udp", host)
	if err != nil {
		return err
	}
	options := []dtls.ClientOption{
		dtls.WithExtendedMasterSecret(dtls.RequireExtendedMasterSecret),
		dtls.WithServerName(u.Hostname()),
	}
	roots, err := loadTransportRootCAs(transportCAPath)
	if err != nil {
		return err
	}
	if roots != nil {
		options = append(options, dtls.WithRootCAs(roots))
	}
	conn, err := dtls.DialWithOptions("udp", uaddr, options...)
	if err != nil {
		return err
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
	b, err := encodePreface(map[string]string{"auth": authToken, "tunnel_id": tunnelID, "dst": udpDst})
	if err != nil {
		return err
	}
	if _, err := conn.Write(b); err != nil {
		return err
	}
	var lastSrcMu sync.RWMutex
	var lastSrc *net.UDPAddr
	errCh := make(chan error, 2)
	startUDPLocalToStream(conn, uc, errCh, &lastSrcMu, &lastSrc)
	startStreamToUDPLocal(bufio.NewReader(conn), uc, errCh, &lastSrcMu, &lastSrc)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}
