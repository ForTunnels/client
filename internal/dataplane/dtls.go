// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"bufio"
	"net"
	"net/url"
	"sync"

	dtls "github.com/pion/dtls/v2"
)

// startDTLSDataPlaneUDP listens on udpListen and forwards via DTLS to server
func StartDTLSDataPlaneUDP(serverURL, tunnelID, authToken, udpDst, udpListen string) error {
	// local UDP listen
	laddr, err := net.ResolveUDPAddr("udp", udpListen)
	if err != nil {
		return err
	}
	uc, err := net.ListenUDP("udp", laddr)
	if err != nil {
		return err
	}
	defer uc.Close()
	// resolve server host and dtls port (from default config 4444)
	u, err := url.Parse(serverURL)
	if err != nil {
		return err
	}
	host := net.JoinHostPort(u.Hostname(), "4444")
	// DTLS dial with proper certificate validation
	dcfg := &dtls.Config{
		InsecureSkipVerify:   false,
		ExtendedMasterSecret: dtls.RequireExtendedMasterSecret,
		ServerName:           u.Hostname(),
	}
	uaddr, err := net.ResolveUDPAddr("udp", host)
	if err != nil {
		return err
	}
	conn, err := dtls.Dial("udp", uaddr, dcfg)
	if err != nil {
		return err
	}
	defer conn.Close()
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
	return <-errCh
}
