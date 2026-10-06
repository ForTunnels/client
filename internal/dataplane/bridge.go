// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"fmt"
	"io"
	"log"

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

// WrapClientStream wraps the stream with encryption if needed.
func WrapClientStream(s io.ReadWriteCloser, tunnelID string, enc config.EncryptionSettings) (io.ReadWriteCloser, error) {
	if !enc.Enabled {
		return s, nil
	}
	mgr := sec.NewClientPSK([]byte(enc.PSK))
	wrapped, err := mgr.Wrap(s, tunnelID)
	if err != nil {
		return nil, fmt.Errorf("wrap client stream: %w", err)
	}
	return wrapped, nil
}

var wrapClientStream = WrapClientStream
