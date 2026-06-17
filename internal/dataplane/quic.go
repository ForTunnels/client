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
	"time"

	"github.com/quic-go/quic-go"
)

const udpReadPollInterval = time.Second

// startQUICDataPlaneUDP listens on udpListen and forwards via QUIC datagrams, receiving replies
func StartQUICDataPlaneUDP(serverURL, quicPort, tunnelID, authToken, udpDst, udpListen string) error {
	laddr, err := net.ResolveUDPAddr("udp", udpListen)
	if err != nil {
		return err
	}
	uc, err := net.ListenUDP("udp", laddr)
	if err != nil {
		return err
	}
	defer uc.Close()

	qc, err := dialQUICConnection(serverURL, quicPort, true)
	if err != nil {
		return err
	}
	defer func() {
		if err := qc.CloseWithError(0, ""); err != nil {
			log.Printf("Error closing QUIC connection: %v", err)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	flows := newFlowRegistry()
	startQUICDatagramReceiver(ctx, cancel, qc, uc, flows)
	return forwardUDPPacketsOverQUIC(ctx, cancel, qc, uc, tunnelID, authToken, udpDst, flows)
}

func startQUICDatagramReceiver(
	ctx context.Context,
	cancel context.CancelFunc,
	qc *quic.Conn,
	uc *net.UDPConn,
	flows *flowRegistry,
) {
	go func() {
		defer cancel()
		for {
			if ctx.Err() != nil {
				return
			}
			b, err := qc.ReceiveDatagram(ctx)
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
				if ra, ok := flows.get(fr.FlowID); ok {
					//nolint:errcheck // best-effort UDP forward
					_, _ = uc.WriteToUDP(fr.Data, ra)
				}
			}
		}
	}()
}

func forwardUDPPacketsOverQUIC(
	ctx context.Context,
	cancel context.CancelFunc,
	qc *quic.Conn,
	uc *net.UDPConn,
	tunnelID, authToken, udpDst string,
	flows *flowRegistry,
) error {
	buf := make([]byte, udpDatagramMaxSize)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := uc.SetReadDeadline(timeFromContext(ctx)); err != nil {
			return err
		}
		n, raddr, err := uc.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			cancel()
			return err
		}
		flowID := raddr.String()
		flows.set(flowID, raddr)
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
			cancel()
			return err
		}
		if err := qc.SendDatagram(b); err != nil {
			cancel()
			return err
		}
	}
}

func timeFromContext(ctx context.Context) time.Time {
	if deadline, ok := ctx.Deadline(); ok {
		return deadline
	}
	return time.Now().Add(udpReadPollInterval)
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
