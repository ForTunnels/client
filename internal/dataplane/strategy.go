// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"fmt"

	"github.com/fortunnels/client/internal/config"
)

const (
	quicDescription = "\n📡 UDP over QUIC: listening on %s and forwarding to %s via QUIC datagrams ...\n"
	dtlsDescription = "\n📡 UDP over DTLS: listening on %s and forwarding to %s via DTLS ...\n"
	wsDescription   = "\n📡 UDP mode: listening on %s and forwarding to %s over WS→smux (preface proto=udp) ...\n"
)

// Strategy encapsulates a UDP data-plane mode.
type Strategy struct {
	Description    string
	RunningMessage string
	StoppedMessage string
	ErrLabel       string
	runner         func() error
}

// Run executes the strategy.
func (s Strategy) Run() error {
	if s.runner == nil {
		return nil
	}
	return s.runner()
}

// NewStrategy builds a strategy for the requested UDP mode.
func NewStrategy(
	kind string,
	serverURL, tunnelID, authToken, dst, listen string,
	runtime config.RuntimeSettings,
	enc config.EncryptionSettings,
) Strategy {
	switch kind {
	case "quic":
		return simpleStrategy(
			fmt.Sprintf(quicDescription, listen, dst),
			"🔌 UDP QUIC tunnel running. Press Ctrl+C to stop.",
			"UDP QUIC tunnel stopped.",
			"udp quic mode error",
			func() error {
				return StartQUICDataPlaneUDP(serverURL, tunnelID, authToken, dst, listen)
			},
		)
	case "dtls":
		return simpleStrategy(
			fmt.Sprintf(dtlsDescription, listen, dst),
			"🔌 UDP DTLS tunnel running. Press Ctrl+C to stop.",
			"UDP DTLS tunnel stopped.",
			"udp dtls mode error",
			func() error {
				return StartDTLSDataPlaneUDP(serverURL, tunnelID, authToken, dst, listen)
			},
		)
	default:
		return simpleStrategy(
			fmt.Sprintf(wsDescription, listen, dst),
			"🔌 UDP tunnel running. Press Ctrl+C to stop.",
			"UDP tunnel stopped.",
			"udp mode error",
			func() error {
				return StartDataPlaneUDP(serverURL, tunnelID, dst, listen, runtime, enc)
			},
		)
	}
}

func simpleStrategy(description, running, stopped, errLabel string, runner func() error) Strategy {
	return Strategy{
		Description:    description,
		RunningMessage: running,
		StoppedMessage: stopped,
		ErrLabel:       errLabel,
		runner:         runner,
	}
}
