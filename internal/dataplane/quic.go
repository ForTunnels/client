// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"log"
	"net"
	"net/url"

	"github.com/quic-go/quic-go"
)

// startQUICDataPlaneUDP listens on udpListen and forwards via QUIC datagrams, receiving replies
func StartQUICDataPlaneUDP(serverURL, tunnelID, authToken, udpDst, udpListen string) error {
	laddr, err := net.ResolveUDPAddr("udp", udpListen)
	if err != nil {
		return err
	}
	uc, err := net.ListenUDP("udp", laddr)
	if err != nil {
		return err
	}
	defer uc.Close()

	qc, err := dialQUICConnection(serverURL, "4433", true)
	if err != nil {
		return err
	}
	defer func() {
		if err := qc.CloseWithError(0, ""); err != nil {
			log.Printf("Error closing QUIC connection: %v", err)
		}
	}()

	flowMap := make(map[string]*net.UDPAddr)
	startQUICDatagramReceiver(qc, uc, flowMap)
	return forwardUDPPacketsOverQUIC(qc, uc, tunnelID, authToken, udpDst, flowMap)
}

func startQUICDatagramReceiver(qc *quic.Conn, uc *net.UDPConn, flowMap map[string]*net.UDPAddr) {
	go func() {
		for {
			b, err := qc.ReceiveDatagram(context.Background())
			if err != nil {
				return
			}
			var fr struct {
				TunnelID string `json:"tunnel_id"`
				FlowID   string `json:"flow_id"`
				Protocol string `json:"protocol"`
				Data     []byte `json:"data"`
			}
			if json.Unmarshal(b, &fr) == nil && fr.Protocol == "udp" && len(fr.Data) > 0 {
				if ra, ok := flowMap[fr.FlowID]; ok {
					//nolint:errcheck // best-effort UDP forward
					_, _ = uc.WriteToUDP(fr.Data, ra)
				}
			}
		}
	}()
}

func forwardUDPPacketsOverQUIC(
	qc *quic.Conn,
	uc *net.UDPConn,
	tunnelID, authToken, udpDst string,
	flowMap map[string]*net.UDPAddr,
) error {
	buf := make([]byte, udpDatagramMaxSize)
	for {
		n, raddr, err := uc.ReadFromUDP(buf)
		if err != nil {
			return err
		}
		flowID := raddr.String()
		flowMap[flowID] = raddr
		frame := map[string]interface{}{
			"tunnel_id": tunnelID,
			"flow_id":   flowID,
			"protocol":  "udp",
			"data":      buf[:n],
			"dst":       udpDst,
			"auth":      authToken,
		}
		b, err := json.Marshal(frame)
		if err != nil {
			return err
		}
		if err := qc.SendDatagram(b); err != nil {
			return err
		}
	}
}

func dialQUICConnection(serverURL, port string, enableDatagrams bool) (*quic.Conn, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return nil, err
	}
	host := net.JoinHostPort(u.Hostname(), port)
	tlsConf := &tls.Config{
		InsecureSkipVerify: false,
		MinVersion:         tls.VersionTLS12,
		NextProtos:         []string{"fortunnels-quic"},
		ServerName:         u.Hostname(),
	}
	quicCfg := &quic.Config{}
	if enableDatagrams {
		quicCfg.EnableDatagrams = true
	}
	return quic.DialAddr(context.Background(), host, tlsConf, quicCfg)
}
