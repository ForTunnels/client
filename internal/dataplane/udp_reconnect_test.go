// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/xtaci/smux"

	"github.com/fortunnels/client/internal/config"
	"github.com/fortunnels/client/shared/wsconn"
)

func TestReverseUDPReconnectsAndForwardsAfterStreamInterruption(t *testing.T) {
	backendAddr, stopBackend := startUDPEchoBackend(t)
	defer stopBackend()
	backendUDPAddr, err := net.ResolveUDPAddr(udpTransport, backendAddr)
	require.NoError(t, err)

	probe, err := net.ListenUDP(udpTransport, &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	require.NoError(t, err)
	listenAddr := probe.LocalAddr().String()
	require.NoError(t, probe.Close())

	firstInterrupted := make(chan struct{})
	secondReady := make(chan struct{})
	fixtureErrors := make(chan error, 2)
	var connectionCount atomic.Int32
	var secondReadyOnce sync.Once
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, upgradeErr := upgrader.Upgrade(w, r, nil)
		if upgradeErr != nil {
			return
		}
		defer conn.Close()

		connectionNumber := connectionCount.Add(1)
		session, sessionErr := smux.Server(wsconn.NewWSConn(conn), smux.DefaultConfig())
		if sessionErr != nil {
			select {
			case fixtureErrors <- sessionErr:
			default:
			}
			return
		}
		defer session.Close()

		stream, streamErr := session.AcceptStream()
		if streamErr != nil {
			select {
			case fixtureErrors <- streamErr:
			default:
			}
			return
		}
		defer stream.Close()

		proto, destination, prefaceErr := readStreamPreface(bufio.NewReader(stream))
		if prefaceErr != nil {
			select {
			case fixtureErrors <- prefaceErr:
			default:
			}
			return
		}
		if proto != udpTransport || destination != backendAddr {
			select {
			case fixtureErrors <- errors.New("reverse UDP stream used an unexpected preface"):
			default:
			}
			return
		}

		if connectionNumber == 1 {
			close(firstInterrupted)
			// Simulate a transport interruption after the stream is established.
			_ = conn.Close()
			return
		}

		backend, dialErr := net.DialUDP(udpTransport, nil, backendUDPAddr)
		if dialErr != nil {
			select {
			case fixtureErrors <- dialErr:
			default:
			}
			return
		}
		defer backend.Close()
		secondReadyOnce.Do(func() { close(secondReady) })
		_ = bridgeUDPStreamAndBackend(stream, backend, destination, nil)
	}))
	defer server.Close()

	runtime := config.RuntimeSettings{
		PingInterval:          30 * time.Second,
		PingTimeout:           5 * time.Second,
		SmuxKeepAliveInterval: 30 * time.Second,
		SmuxKeepAliveTimeout:  90 * time.Second,
	}
	ctx, cancel := context.WithCancel(context.Background())
	strategy := NewStrategy(
		"ws", server.URL, "qa-reverse-udp", "qa-auth", backendAddr, listenAddr,
		runtime, config.EncryptionSettings{}, 10*time.Millisecond, 20*time.Millisecond,
	)
	done := make(chan error, 1)
	go func() { done <- strategy.RunContext(ctx) }()
	runStopped := false
	defer func() {
		cancel()
		if runStopped {
			return
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("reverse UDP strategy did not stop after context cancellation")
		}
	}()

	awaitSignal := func(name string, signal <-chan struct{}) {
		t.Helper()
		select {
		case <-signal:
		case fixtureErr := <-fixtureErrors:
			t.Fatalf("local data-plane fixture failed before %s: %v", name, fixtureErr)
		case <-time.After(5 * time.Second):
			t.Fatalf("reverse UDP strategy did not reach %s; WebSocket sessions=%d", name, connectionCount.Load())
		}
	}
	awaitSignal("first interrupted stream", firstInterrupted)
	awaitSignal("reconnected stream", secondReady)

	client, err := net.DialUDP(udpTransport, nil, mustResolveUDPAddr(t, listenAddr))
	require.NoError(t, err)
	defer client.Close()
	require.NoError(t, client.SetDeadline(time.Now().Add(3*time.Second)))
	payload := []byte("qa-reverse-udp-after-reconnect")
	_, err = client.Write(payload)
	require.NoError(t, err)
	response := make([]byte, 128)
	n, _, err := client.ReadFromUDP(response)
	require.NoError(t, err)
	require.Equal(t, payload, response[:n], "server-side UDP echo must return through the recovered data plane")

	cancel()
	select {
	case runErr := <-done:
		runStopped = true
		require.ErrorIs(t, runErr, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("reverse UDP strategy remained blocked after cancellation")
	}
}

func mustResolveUDPAddr(t *testing.T, address string) *net.UDPAddr {
	t.Helper()
	resolved, err := net.ResolveUDPAddr(udpTransport, address)
	require.NoError(t, err)
	return resolved
}
