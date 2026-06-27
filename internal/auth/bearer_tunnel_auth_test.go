// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTunnelLooksLikeGuest(t *testing.T) {
	t.Parallel()

	assert.True(t, TunnelLooksLikeGuest(TunnelGuestSignals{IsGuest: true, UserID: 42}))
	assert.True(t, TunnelLooksLikeGuest(TunnelGuestSignals{UserID: 0}))
	assert.False(t, TunnelLooksLikeGuest(TunnelGuestSignals{UserID: 42}))
}

func TestCheckBearerNotRejectedAsGuest(t *testing.T) {
	t.Parallel()

	t.Run("empty bearer", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, CheckBearerNotRejectedAsGuest("", true, TunnelGuestSignals{IsGuest: true}))
	})

	t.Run("owned tunnel", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, CheckBearerNotRejectedAsGuest("ft_live", true, TunnelGuestSignals{UserID: 42}))
	})

	t.Run("guest via omitted is_guest", func(t *testing.T) {
		t.Parallel()
		err := CheckBearerNotRejectedAsGuest("ft_revoked", true, TunnelGuestSignals{UserID: 0})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "config add-authtoken")
	})

	t.Run("config file bearer rejected", func(t *testing.T) {
		t.Parallel()
		err := CheckBearerNotRejectedAsGuest("ft_revoked", true, TunnelGuestSignals{IsGuest: true})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "config add-authtoken")
		assert.Contains(t, err.Error(), "agent.authtoken")
	})

	t.Run("explicit bearer rejected", func(t *testing.T) {
		t.Parallel()
		err := CheckBearerNotRejectedAsGuest("ft_revoked", false, TunnelGuestSignals{IsGuest: true})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "config add-authtoken")
		assert.Contains(t, err.Error(), "FORTUNNELS_TOKEN")
	})
}
