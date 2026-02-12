// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"io"
	"log"
	"net"
	"strings"

	"github.com/fortunnels/client/internal/config"
	sec "github.com/fortunnels/client/internal/security"
)

// SafeClose closes the given io.Closer and logs any error.
// This is suitable for cleanup operations where we don't want to fail the main operation.
func SafeClose(c io.Closer) {
	if c != nil {
		if err := c.Close(); err != nil {
			log.Printf("error closing resource: %v", err)
		}
	}
}

// PipeStreams bridges two connections with backpressure-aware buffers.
func PipeStreams(a net.Conn, b io.ReadWriteCloser) {
	bufA := make([]byte, 64*1024)
	bufB := make([]byte, 64*1024)
	done := make(chan struct{}, 2)
	startBufferedCopy(a, b, bufB, "b->a", done)
	startBufferedCopy(b, a, bufA, "a->b", done)
	<-done
}

func startBufferedCopy(dst io.Writer, src io.Reader, buf []byte, label string, done chan<- struct{}) {
	go func() {
		_, err := io.CopyBuffer(dst, src, buf)
		if err != nil && err != io.EOF && !isClosedPipe(err) {
			log.Printf("client bridge: copy %s error: %v", label, err)
		}
		done <- struct{}{}
	}()
}

func isClosedPipe(err error) bool {
	return strings.Contains(err.Error(), "closed pipe")
}

// WrapClientStream wraps the stream with encryption if needed.
func WrapClientStream(s io.ReadWriteCloser, tunnelID string, enc config.EncryptionSettings) io.ReadWriteCloser {
	if !enc.Enabled {
		return s
	}
	mgr := sec.NewClientPSK([]byte(enc.PSK))
	return mgr.Wrap(s, tunnelID)
}
