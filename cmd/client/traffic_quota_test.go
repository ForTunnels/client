// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	ctrl "github.com/fortunnels/client/internal/control"
)

type commandQuotaOutput struct{ bytes.Buffer }

func (o *commandQuotaOutput) Printf(format string, args ...any) { _, _ = o.Buffer.WriteString(format) }
func (o *commandQuotaOutput) Println(args ...any)               { _, _ = o.Buffer.WriteString("reported\n") }

func TestTrafficQuota_CommandExitsNonzeroWithoutDuplicateMessage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	out := &commandQuotaOutput{}
	coordinator := ctrl.NewTerminalCoordinator(out, cancel)
	coordinator.Signal(&ctrl.TrafficQuotaError{})
	<-coordinator.Done()

	errCh := make(chan error, 1)
	errCh <- context.Canceled
	err := waitForProtocol(coordinator, errCh, nil)
	require.Error(t, err, "a reported terminal error makes main exit nonzero")
	var stderr bytes.Buffer
	writeWorkflowError(&stderr, err)
	require.Empty(t, stderr.String(), "main must not print a coordinator-reported error twice")
	require.Equal(t, "reported\n", out.String())
	require.ErrorIs(t, ctx.Err(), context.Canceled)
}

func TestTrafficQuota_OrdinaryRemovalPreservesSuccessfulExit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	coordinator := ctrl.NewTerminalCoordinator(&commandQuotaOutput{}, cancel)
	coordinator.Signal(nil)

	errCh := make(chan error, 1)
	errCh <- context.Canceled
	require.NoError(t, waitForProtocol(coordinator, errCh, nil))
	require.ErrorIs(t, ctx.Err(), context.Canceled)
}
