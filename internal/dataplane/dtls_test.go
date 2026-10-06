// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type dtlsPacketTestWriter struct{ packets chan []byte }

func (w *dtlsPacketTestWriter) Write(buf []byte) (int, error) {
	w.packets <- bytes.Clone(buf)
	return len(buf), nil
}

type dtlsPacketTestReader struct{ packets chan []byte }

func (r *dtlsPacketTestReader) Read(buf []byte) (int, error) {
	packet, ok := <-r.packets
	if !ok {
		return 0, io.EOF
	}
	return copy(buf, packet), nil
}

func dtlsTestRecord(size int) []byte {
	frame := make([]byte, 2+size)
	binary.BigEndian.PutUint16(frame[:2], uint16(size))
	copy(frame[2:], bytes.Repeat([]byte{'x'}, size))
	return frame
}

func TestWriteUDPDatagramRecord_SingleWrite(t *testing.T) {
	w := &dtlsPacketTestWriter{packets: make(chan []byte, 2)}
	require.NoError(t, writeUDPDatagramRecord(w, []byte("xx")))
	require.Equal(t, []byte{0, 2, 'x', 'x'}, <-w.packets)
	require.Empty(t, w.packets)
}

func TestWriteUDPDatagramRecord_PayloadLimit(t *testing.T) {
	w := &dtlsPacketTestWriter{packets: make(chan []byte, 2)}
	require.NoError(t, writeUDPDatagramRecord(w, bytes.Repeat([]byte{'x'}, 8137)))
	require.Equal(t, dtlsTestRecord(8137), <-w.packets)
	require.ErrorIs(t, writeUDPDatagramRecord(w, bytes.Repeat([]byte{'x'}, 8138)), errDTLSRecordPayloadTooLarge)
	require.Empty(t, w.packets, "oversize must never reach Write")
}

func TestReadUDPDatagramRecord_RejectsLengthMismatch(t *testing.T) {
	for _, frame := range [][]byte{{0}, {0, 5, 'x'}} {
		_, err := readUDPDatagramRecord(bytes.NewReader(frame), make([]byte, udpMaxPacketSize))
		require.ErrorIs(t, err, errInvalidDTLSRecord)
	}
}

func TestReadUDPDatagramRecord_RoundTrip(t *testing.T) {
	for _, size := range []int{4, 8137} {
		packet, err := readUDPDatagramRecord(bytes.NewReader(dtlsTestRecord(size)), make([]byte, udpMaxPacketSize))
		require.NoError(t, err)
		require.Equal(t, bytes.Repeat([]byte{'x'}, size), packet)
	}
}

func TestReadUDPDatagramRecord_RejectsOversizedPayload(t *testing.T) {
	_, err := readUDPDatagramRecord(bytes.NewReader(dtlsTestRecord(8138)), make([]byte, udpMaxPacketSize))
	require.ErrorIs(t, err, errDTLSRecordPayloadTooLarge)
}

func TestStartDTLSDataPlaneUDPInvalidURL(t *testing.T) {
	err := startDTLSDataPlaneUDPContext(context.Background(), "://invalid", "4444", "t1", "", "127.0.0.1:1", "127.0.0.1:0", "")
	require.Error(t, err)
	var permanent permanentError
	require.True(t, errors.As(err, &permanent))
}

func dtlsTestLocalSocket(t *testing.T) *net.UDPConn {
	t.Helper()
	s, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

type dtlsDropTestLog struct{ dropped chan struct{} }

func (w dtlsDropTestLog) Write(buf []byte) (int, error) {
	if strings.Contains(string(buf), "size=8138") {
		select {
		case w.dropped <- struct{}{}:
		default:
		}
	}
	return len(buf), nil
}

func TestStartDTLSDataPlaneUDP_DropsOversizedPayloadAndContinues(t *testing.T) {
	local, first, second := dtlsTestLocalSocket(t), dtlsTestLocalSocket(t), dtlsTestLocalSocket(t)
	w := &dtlsPacketTestWriter{packets: make(chan []byte, 2)}
	dropped := make(chan struct{}, 1)
	oldLog := log.Writer()
	log.SetOutput(dtlsDropTestLog{dropped: dropped})
	t.Cleanup(func() { log.SetOutput(oldLog) })
	var mu sync.RWMutex
	var lastSrc *net.UDPAddr
	errCh := make(chan error, 2)
	startUDPLocalToPackets(func(payload []byte) error { return writeUDPDatagramRecord(w, payload) },
		maxDTLSRecordPayload, local, errCh, &mu, &lastSrc)
	send := func(sender *net.UDPConn, size int) {
		_, err := sender.WriteToUDP(bytes.Repeat([]byte{'x'}, size), local.LocalAddr().(*net.UDPAddr))
		require.NoError(t, err)
	}
	awaitWrite := func(size int) {
		select {
		case frame := <-w.packets:
			require.Equal(t, dtlsTestRecord(size), frame)
		case <-time.After(time.Second):
			t.Fatal("producer stopped after packet drop")
		}
	}
	send(first, 8137)
	awaitWrite(8137)
	send(second, 8138)
	select {
	case <-dropped:
	case <-time.After(time.Second):
		t.Fatal("oversize was not consumed")
	}
	mu.RLock()
	require.Equal(t, first.LocalAddr().String(), lastSrc.String(), "drop must preserve accepted reply mapping")
	mu.RUnlock()
	require.Empty(t, w.packets)
	require.Empty(t, errCh, "drop must not terminate or retry attempt")
	send(second, 4)
	awaitWrite(4)
	mu.RLock()
	require.Equal(t, second.LocalAddr().String(), lastSrc.String())
	mu.RUnlock()
	require.NoError(t, local.Close())
	select {
	case <-errCh:
	case <-time.After(time.Second):
		t.Fatal("producer did not release socket")
	}
}

func TestStartDTLSDataPlaneUDP_DropsOversizedReceivedRecordAndContinues(t *testing.T) {
	local, receiver := dtlsTestLocalSocket(t), dtlsTestLocalSocket(t)
	reader := &dtlsPacketTestReader{packets: make(chan []byte, 3)}
	reader.packets <- dtlsTestRecord(8138)
	reader.packets <- dtlsTestRecord(4)
	var mu sync.RWMutex
	lastSrc := receiver.LocalAddr().(*net.UDPAddr)
	errCh := make(chan error, 2)
	buf := make([]byte, udpMaxPacketSize)
	startPacketsToUDPLocal(func() ([]byte, error) { return readUDPDatagramRecord(reader, buf) }, local, errCh, &mu, &lastSrc)
	require.NoError(t, receiver.SetReadDeadline(time.Now().Add(time.Second)))
	out := make([]byte, udpMaxPacketSize)
	n, _, err := receiver.ReadFromUDP(out)
	require.NoError(t, err)
	require.Equal(t, "xxxx", string(out[:n]))
	require.Empty(t, errCh)
	close(reader.packets)
	select {
	case err = <-errCh:
		require.ErrorIs(t, err, io.EOF)
	case <-time.After(time.Second):
		t.Fatal("receiver did not stop")
	}
}
