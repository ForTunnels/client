// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"context"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/xtaci/smux"

	"github.com/fortunnels/client/internal/config"
	"github.com/fortunnels/client/shared/wsconn"
)

type wsKeepaliveConn interface {
	SetReadDeadline(time.Time) error
	SetPongHandler(func(string) error)
}

func configureWSReadKeepalive(conn wsKeepaliveConn) {
	//nolint:errcheck // best-effort read deadline
	_ = conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
	conn.SetPongHandler(func(string) error {
		//nolint:errcheck // pong handler best-effort deadline refresh
		_ = conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
		return nil
	})
}

func setupWSSmuxSession(conn *websocket.Conn, settings config.RuntimeSettings) (*smux.Session, error) {
	configureWSReadKeepalive(conn)

	cfg := smux.DefaultConfig()
	cfg.KeepAliveInterval = settings.SmuxKeepAliveInterval
	cfg.KeepAliveTimeout = settings.SmuxKeepAliveTimeout

	sess, err := smux.Client(wsconn.NewWSConn(conn), cfg)
	if err != nil {
		return nil, err
	}
	return sess, nil
}

// StartPingLoop sends WebSocket ping frames until done is closed.
func StartPingLoop(done <-chan struct{}, conn *websocket.Conn, ticker *time.Ticker, pingTimeout time.Duration) {
	go func() {
		for {
			select {
			case <-ticker.C:
				deadline := time.Now().Add(pingTimeout)
				//nolint:errcheck // best-effort ping
				_ = conn.WriteControl(websocket.PingMessage, nil, deadline)
			case <-done:
				return
			}
		}
	}()
}

// StartControlPingLoop sends pings and closes done when a ping write fails.
func StartControlPingLoop(
	done chan struct{},
	doneOnce *sync.Once,
	conn *websocket.Conn,
	ticker *time.Ticker,
	pingTimeout time.Duration,
) {
	go func() {
		for {
			select {
			case <-ticker.C:
				deadline := time.Now().Add(pingTimeout)
				if err := conn.WriteControl(websocket.PingMessage, nil, deadline); err != nil {
					doneOnce.Do(func() { close(done) })
					return
				}
			case <-done:
				return
			}
		}
	}()
}

func sleepReconnectBackoffContext(ctx context.Context, stopped func() bool, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil && !stopped()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	pollEvery := min(d, 50*time.Millisecond)
	poll := time.NewTicker(pollEvery)
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return !stopped()
		case <-poll.C:
		}
		if stopped() {
			return false
		}
	}
}
