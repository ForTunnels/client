// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDialQUICConnectionInvalidURL(t *testing.T) {
	if _, err := dialQUICConnection("://bad", "4433", false); err == nil {
		t.Fatalf("dialQUICConnection() expected error for invalid URL")
	}
}

func TestForwardUDPPacketsOverQUIC_ContextCancel(t *testing.T) {
	uc, err := listenTestUDP("127.0.0.1:0")
	require.NoError(t, err)
	defer uc.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- forwardUDPPacketsOverQUIC(ctx, cancel, nil, uc, "t1", "auth", "127.0.0.1:53", map[string]*net.UDPAddr{})
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("forward loop did not exit after context cancel")
	}
}

func listenTestUDP(addr string) (*net.UDPConn, error) {
	laddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	return net.ListenUDP("udp", laddr)
}
