// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import "testing"

func TestStartDTLSDataPlaneUDPInvalidURL(t *testing.T) {
	err := StartDTLSDataPlaneUDP("://bad", "tid", "auth", "127.0.0.1:53", "127.0.0.1:0")
	if err == nil {
		t.Fatalf("StartDTLSDataPlaneUDP() expected error for invalid URL")
	}
}
