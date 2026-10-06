// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/stretchr/testify/require"
)

type oversizedDatagramConn struct {
	dropsRemaining int
	accepted       chan []byte
}

func (c *oversizedDatagramConn) SendDatagram(b []byte) error {
	if c.dropsRemaining > 0 {
		c.dropsRemaining--
		return &quic.DatagramTooLargeError{MaxDatagramPayloadSize: 1200}
	}
	c.accepted <- append([]byte(nil), b...)
	return nil
}

func (*oversizedDatagramConn) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestForwardUDPPacketsOverQUIC_DropsOversizedDatagramAndContinues(t *testing.T) {
	uc, err := listenTestUDP()
	require.NoError(t, err)
	defer uc.Close()
	peer, err := net.DialUDP("udp", nil, uc.LocalAddr().(*net.UDPAddr))
	require.NoError(t, err)
	defer peer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var logs bytes.Buffer
	previousOutput := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previousOutput)
	conn := &oversizedDatagramConn{dropsRemaining: 100, accepted: make(chan []byte, 1)}
	done := make(chan error, 1)
	go func() {
		done <- forwardUDPPacketsOverQUIC(ctx, cancel, conn, uc, "t1", "secret-auth", "127.0.0.1:53", newFlowRegistry())
	}()
	for i := 0; i < 100; i++ {
		_, writeErr := peer.Write([]byte("oversized"))
		require.NoError(t, writeErr)
	}
	_, err = peer.Write([]byte("next"))
	require.NoError(t, err)
	select {
	case frame := <-conn.accepted:
		var payload struct {
			Data []byte `json:"data"`
		}
		require.NoError(t, json.Unmarshal(frame, &payload))
		require.Equal(t, []byte("next"), payload.Data)
	case <-time.After(2 * time.Second):
		t.Fatal("next datagram was not sent")
	}
	select {
	case err := <-done:
		t.Fatalf("forward loop ended after drop: %v", err)
	default:
	}
	cancel()
	_ = uc.Close()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("forward loop did not stop")
	}
	require.Contains(t, logs.String(), "dropped=1\n")
	require.Contains(t, logs.String(), "dropped=100\n")
	require.Equal(t, 2, bytes.Count(logs.Bytes(), []byte("QUIC datagram dropped")))
	require.NotContains(t, logs.String(), "secret-auth")
}

type pendingQUICDatagram struct {
	data   []byte
	result chan error
}

type controlledDatagramConn struct {
	ctx     context.Context
	sent    chan pendingQUICDatagram
	replies chan []byte
}

func (c *controlledDatagramConn) SendDatagram(b []byte) error {
	send := pendingQUICDatagram{data: append([]byte(nil), b...), result: make(chan error, 1)}
	select {
	case c.sent <- send:
	case <-c.ctx.Done():
		return c.ctx.Err()
	}
	select {
	case err := <-send.result:
		return err
	case <-c.ctx.Done():
		return c.ctx.Err()
	}
}

func (c *controlledDatagramConn) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	select {
	case b := <-c.replies:
		return b, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestForwardUDPPacketsOverQUIC_RejectedSourcesDoNotAccumulate(t *testing.T) {
	uc, err := listenTestUDP()
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer uc.Close()
	conn := &controlledDatagramConn{ctx: ctx, sent: make(chan pendingQUICDatagram), replies: make(chan []byte, 1)}
	flows := newFlowRegistry()
	startQUICDatagramReceiver(ctx, cancel, conn, uc, flows)
	done := make(chan error, 1)
	go func() {
		done <- forwardUDPPacketsOverQUIC(ctx, cancel, conn, uc, "t1", "auth", "127.0.0.1:53", flows)
	}()
	newPeer := func() *net.UDPConn {
		peer, dialErr := net.DialUDP("udp", nil, uc.LocalAddr().(*net.UDPAddr))
		require.NoError(t, dialErr)
		t.Cleanup(func() { _ = peer.Close() })
		return peer
	}
	sendPacket := func(peer *net.UDPConn, data string) pendingQUICDatagram {
		_, writeErr := peer.Write([]byte(data))
		require.NoError(t, writeErr)
		select {
		case send := <-conn.sent:
			return send
		case <-time.After(time.Second):
			t.Fatal("forward loop did not send datagram")
			return pendingQUICDatagram{}
		}
	}
	assertReplyBeforeSendReturns := func(peer *net.UDPConn, send pendingQUICDatagram) {
		var frame struct {
			FlowID   string `json:"flow_id"`
			Protocol string `json:"protocol"`
			Data     []byte `json:"data"`
		}
		require.NoError(t, json.Unmarshal(send.data, &frame))
		require.Equal(t, peer.LocalAddr().String(), frame.FlowID)
		frame.Data = []byte("reply")
		reply, marshalErr := json.Marshal(frame)
		require.NoError(t, marshalErr)
		conn.replies <- reply
		require.NoError(t, peer.SetReadDeadline(time.Now().Add(time.Second)))
		buf := make([]byte, 16)
		n, readErr := peer.Read(buf)
		require.NoError(t, readErr, "reply must arrive while SendDatagram is still pending")
		require.Equal(t, "reply", string(buf[:n]))
		send.result <- nil
	}
	acceptedPeer := newPeer()
	assertReplyBeforeSendReturns(acceptedPeer, sendPacket(acceptedPeer, "accepted"))
	establishedAddr, exists := flows.get(acceptedPeer.LocalAddr().String())
	require.True(t, exists)
	tooLarge := &quic.DatagramTooLargeError{MaxDatagramPayloadSize: 1200}
	sendPacket(acceptedPeer, "oversized-established").result <- tooLarge
	rejectedIDs := make([]string, 0, 32)
	for i := 0; i < 32; i++ {
		peer := newPeer()
		rejectedIDs = append(rejectedIDs, peer.LocalAddr().String())
		sendPacket(peer, "oversized-new").result <- tooLarge
	}
	// The next send is a barrier: every earlier rejection has been processed.
	finalPeer := newPeer()
	finalSend := sendPacket(finalPeer, "accepted-after-drops")
	for _, flowID := range rejectedIDs {
		_, retained := flows.get(flowID)
		require.False(t, retained, "rejected source %s retained", flowID)
	}
	addr, exists := flows.get(acceptedPeer.LocalAddr().String())
	require.True(t, exists)
	require.Same(t, establishedAddr, addr, "an established mapping must survive rejection")
	flows.mu.RLock()
	flowCount := len(flows.flows)
	flows.mu.RUnlock()
	require.Equal(t, 2, flowCount, "only established and pending accepted sources may remain")
	assertReplyBeforeSendReturns(finalPeer, finalSend)
	cancel()
	_ = uc.Close()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("forward loop did not stop")
	}
}

func TestForwardUDPPacketsOverQUIC_FatalSendDiscardsNewSource(t *testing.T) {
	uc, err := listenTestUDP()
	require.NoError(t, err)
	defer uc.Close()
	peer, err := net.DialUDP("udp", nil, uc.LocalAddr().(*net.UDPAddr))
	require.NoError(t, err)
	defer peer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn := &controlledDatagramConn{ctx: ctx, sent: make(chan pendingQUICDatagram)}
	flows := newFlowRegistry()
	done := make(chan error, 1)
	go func() {
		done <- forwardUDPPacketsOverQUIC(ctx, cancel, conn, uc, "t1", "auth", "127.0.0.1:53", flows)
	}()
	_, err = peer.Write([]byte("first"))
	require.NoError(t, err)
	sendErr := errors.New("connection closed")
	select {
	case send := <-conn.sent:
		send.result <- sendErr
	case <-time.After(time.Second):
		t.Fatal("forward loop did not send datagram")
	}
	select {
	case err := <-done:
		require.ErrorIs(t, err, sendErr)
	case <-time.After(time.Second):
		t.Fatal("fatal send did not stop forward loop")
	}
	_, exists := flows.get(peer.LocalAddr().String())
	require.False(t, exists)
	require.ErrorIs(t, ctx.Err(), context.Canceled)
}

func TestForwardUDPPacketsOverQUIC_ContextCancel(t *testing.T) {
	uc, err := listenTestUDP()
	require.NoError(t, err)
	defer uc.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- forwardUDPPacketsOverQUIC(ctx, cancel, nil, uc, "t1", "auth", "127.0.0.1:53", newFlowRegistry())
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

func TestFlowRegistry_ConcurrentAccess(t *testing.T) {
	reg := newFlowRegistry()
	addr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5353}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			flowID := fmt.Sprintf("flow-%d", id)
			reg.set(flowID, addr)
			_, _ = reg.get(flowID)
		}(i)
	}
	wg.Wait()
}

func listenTestUDP() (*net.UDPConn, error) {
	laddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	return net.ListenUDP("udp", laddr)
}
