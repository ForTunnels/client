// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/url"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
)

const (
	udpReadPollInterval = time.Second
	udpTransport        = "udp"
)

// startQUICDataPlaneUDP listens on udpListen and forwards via QUIC datagrams, receiving replies
func StartQUICDataPlaneUDP(serverURL, quicPort, tunnelID, authToken, udpDst, udpListen string) error {
	return StartQUICDataPlaneUDPContext(context.Background(), serverURL, quicPort, tunnelID, authToken, udpDst, udpListen)
}

func StartQUICDataPlaneUDPContext(ctx context.Context, serverURL, quicPort, tunnelID, authToken, udpDst, udpListen string) error {
	return startQUICDataPlaneUDPContext(ctx, serverURL, quicPort, tunnelID, authToken, udpDst, udpListen, "")
}

func startQUICDataPlaneUDPContext(
	ctx context.Context,
	serverURL, quicPort, tunnelID, authToken, udpDst, udpListen, transportCAPath string,
) error {
	laddr, err := net.ResolveUDPAddr(udpTransport, udpListen)
	if err != nil {
		return err
	}
	uc, err := net.ListenUDP(udpTransport, laddr)
	if err != nil {
		return err
	}

	qc, err := dialQUICConnectionWithCA(serverURL, quicPort, true, transportCAPath)
	if err != nil {
		return err
	}
	var closeOnce sync.Once
	closeResources := func() {
		closeOnce.Do(func() {
			_ = uc.Close()
			if closeErr := qc.CloseWithError(0, ""); closeErr != nil {
				log.Printf("Error closing QUIC connection: %v", closeErr)
			}
		})
	}
	defer closeResources()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopCloser := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			closeResources()
		case <-stopCloser:
		}
	}()
	defer close(stopCloser)

	flows := newFlowRegistry()
	startQUICDatagramReceiver(ctx, cancel, qc, uc, flows)
	err = forwardUDPPacketsOverQUIC(ctx, cancel, qc, uc, tunnelID, authToken, udpDst, flows)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
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
			if json.Unmarshal(b, &fr) == nil && fr.Protocol == udpTransport && len(fr.Data) > 0 {
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
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
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
			"protocol":  udpTransport,
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
	return dialQUICConnectionWithCA(serverURL, port, enableDatagrams, "")
}

func dialQUICConnectionWithCA(serverURL, port string, enableDatagrams bool, transportCAPath string) (*quic.Conn, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return nil, err
	}
	host := net.JoinHostPort(u.Hostname(), port)
	tlsConf, err := newTransportTLSConfig(serverURL, transportCAPath)
	if err != nil {
		return nil, err
	}
	quicCfg := &quic.Config{}
	if enableDatagrams {
		quicCfg.EnableDatagrams = true
	}
	return quic.DialAddr(context.Background(), host, tlsConf, quicCfg)
}
