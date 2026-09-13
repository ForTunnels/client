// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBackendStateReporter_ReportsOneTransitionPerDestination(t *testing.T) {
	var output bytes.Buffer
	reporter := newBackendStateReporter("http", &output)
	down := errors.New("connection refused")

	reporter("localhost:4321", down)
	reporter("localhost:4321", down)
	reporter("localhost:4322", down)
	reporter("localhost:4321", nil)
	reporter("localhost:4321", nil)
	reporter("localhost:4322", nil)

	got := output.String()
	require.Equal(t, 1, strings.Count(got, "Backend unreachable for localhost:4321 (HTTP)"))
	require.Equal(t, 1, strings.Count(got, "Backend unreachable for localhost:4322 (HTTP)"))
	require.Equal(t, 1, strings.Count(got, "Backend reachable for localhost:4321 (HTTP)"))
	require.Equal(t, 1, strings.Count(got, "Backend reachable for localhost:4322 (HTTP)"))
	require.Contains(t, got, "tunnel remains active; the next incoming HTTP request retries this backend")
}

func TestBackendStateReporter_NilOutputDoesNotPanic(t *testing.T) {
	reporter := newBackendStateReporter("tcp", nil)
	require.NotPanics(t, func() {
		reporter("localhost:5432", errors.New("connection refused"))
	})
}
