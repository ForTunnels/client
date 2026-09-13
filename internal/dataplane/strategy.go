// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"context"
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
	ErrLabel       string
	runner         func() error
	contextRunner  func(context.Context) error
}

func (s Strategy) RunContext(ctx context.Context) error {
	if s.contextRunner != nil {
		return s.contextRunner(ctx)
	}
	return s.Run()
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
	case "quic", "dtls":
		return secureUDPStrategy(kind, serverURL, tunnelID, authToken, dst, listen, runtime)
	default:
		return contextStrategy(
			fmt.Sprintf(wsDescription, listen, dst),
			"🔌 UDP tunnel running. Press Ctrl+C to stop.",
			"udp mode error",
			func() error {
				return StartDataPlaneUDP(serverURL, tunnelID, dst, listen, runtime, enc, authToken)
			},
			func(ctx context.Context) error {
				return StartDataPlaneUDPContext(ctx, serverURL, tunnelID, dst, listen, runtime, enc, authToken)
			},
		)
	}
}

func secureUDPStrategy(
	kind string,
	serverURL, tunnelID, authToken, dst, listen string,
	runtime config.RuntimeSettings,
) Strategy {
	description := quicDescription
	running := "🔌 UDP QUIC tunnel running. Press Ctrl+C to stop."
	errLabel := "udp quic mode error"
	port := runtime.QUICPortString()
	start := startQUICDataPlaneUDPContext
	if kind == "dtls" {
		description = dtlsDescription
		running = "🔌 UDP DTLS tunnel running. Press Ctrl+C to stop."
		errLabel = "udp dtls mode error"
		port = runtime.DTLSPortString()
		start = startDTLSDataPlaneUDPContext
	}
	return contextStrategy(
		fmt.Sprintf(description, listen, dst),
		running,
		errLabel,
		func() error {
			return start(context.Background(), serverURL, port, tunnelID, authToken, dst, listen, runtime.TransportCAPath)
		},
		func(ctx context.Context) error {
			return start(ctx, serverURL, port, tunnelID, authToken, dst, listen, runtime.TransportCAPath)
		},
	)
}

func simpleStrategy(description, running, errLabel string, runner func() error) Strategy {
	return contextStrategy(description, running, errLabel, runner, nil)
}

func contextStrategy(description, running, errLabel string, runner func() error, contextRunner func(context.Context) error) Strategy {
	return Strategy{
		Description:    description,
		RunningMessage: running,
		ErrLabel:       errLabel,
		runner:         runner,
		contextRunner:  contextRunner,
	}
}
