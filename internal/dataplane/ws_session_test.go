// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"testing"

	"github.com/fortunnels/client/internal/config"
)

func TestWSReadTimeoutExceedsTwiceMaxPingInterval(t *testing.T) {
	if wsReadTimeout <= 2*config.MaxPingInterval {
		t.Fatalf("ws read timeout %s must exceed twice the maximum ping interval %s", wsReadTimeout, 2*config.MaxPingInterval)
	}
}
