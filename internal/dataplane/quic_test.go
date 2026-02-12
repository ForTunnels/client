// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import "testing"

func TestDialQUICConnectionInvalidURL(t *testing.T) {
	if _, err := dialQUICConnection("://bad", "4433", false); err == nil {
		t.Fatalf("dialQUICConnection() expected error for invalid URL")
	}
}
