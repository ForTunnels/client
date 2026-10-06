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

type quicDatagramConn interface {
	SendDatagram([]byte) error
	ReceiveDatagram(context.Context) ([]byte, error)
}

// startQUICDataPlaneUDP listens on udpListen and forwards via QUIC datagrams, receiving replies
func startQUICDataPlaneUDPContext(
	ctx context.Context,
	serverURL, quicPort, tunnelID, authToken, udpDst, udpListen, transportCAPath string,
) error {
	laddr, err := net.ResolveUDPAddr(udpTransport, udpListen)
	if err != nil {
		return permanentError{err}
	}
	uc, err := net.ListenUDP(udpTransport, laddr)
	if err != nil {
		return permanentError{err}
	}
	defer uc.Close()

	qc, err := dialQUICConnectionWithCA(ctx, serverURL, quicPort, true, transportCAPath)
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
	qc quicDatagramConn,
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
	qc quicDatagramConn,
	uc *net.UDPConn,
	tunnelID, authToken, udpDst string,
	flows *flowRegistry,
) error {
	buf := make([]byte, udpDatagramMaxSize)
	var dropped uint64
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
		// Register before sending so a prompt reply can find the source. A rejected
		// first packet must not leave state behind for the lifetime of the session.
		newFlow := flows.registerQUICFlow(flowID, raddr)
		if err := qc.SendDatagram(b); err != nil {
			if newFlow {
				flows.discardQUICFlow(flowID, raddr)
			}
			var tooLarge *quic.DatagramTooLargeError
			if errors.As(err, &tooLarge) {
				dropped++
				if dropped == 1 || dropped%100 == 0 {
					log.Printf("QUIC datagram dropped: payload size=%d max=%d dropped=%d", len(b), tooLarge.MaxDatagramPayloadSize, dropped)
				}
				continue
			}
			cancel()
			return err
		}
	}
}

func (r *flowRegistry) registerQUICFlow(flowID string, addr *net.UDPAddr) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.flows[flowID]; exists {
		return false
	}
	r.flows[flowID] = addr
	return true
}

func (r *flowRegistry) discardQUICFlow(flowID string, addr *net.UDPAddr) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.flows[flowID] == addr {
		delete(r.flows, flowID)
	}
}

func timeFromContext(ctx context.Context) time.Time {
	if deadline, ok := ctx.Deadline(); ok {
		return deadline
	}
	return time.Now().Add(udpReadPollInterval)
}

func dialQUICConnectionWithCA(ctx context.Context, serverURL, port string, enableDatagrams bool, transportCAPath string) (*quic.Conn, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return nil, permanentError{err}
	}
	host := net.JoinHostPort(u.Hostname(), port)
	tlsConf, err := newTransportTLSConfig(serverURL, transportCAPath)
	if err != nil {
		return nil, permanentError{err}
	}
	quicCfg := &quic.Config{}
	if enableDatagrams {
		quicCfg.EnableDatagrams = true
	}
	conn, err := quic.DialAddr(ctx, host, tlsConf, quicCfg)
	if err != nil {
		return nil, classifyTransportDialError(err)
	}
	return conn, nil
}
