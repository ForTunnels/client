// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package auth

import (
	"fmt"
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

func TestMapCreateTunnelAuthError(t *testing.T) {
	t.Parallel()

	t.Run("401 with config authtoken", func(t *testing.T) {
		t.Parallel()
		createErr := fmt.Errorf("server returned status 401: unauthorized")
		err := MapCreateTunnelAuthError(createErr, "ft_revoked", true)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "saved authtoken")
		assert.Contains(t, err.Error(), "config add-authtoken")
	})

	t.Run("401 without bearer", func(t *testing.T) {
		t.Parallel()
		createErr := fmt.Errorf("server returned status 401: unauthorized")
		require.NoError(t, MapCreateTunnelAuthError(createErr, "", true))
	})

	t.Run("non-401 with bearer", func(t *testing.T) {
		t.Parallel()
		createErr := fmt.Errorf("server returned status 403: forbidden")
		require.NoError(t, MapCreateTunnelAuthError(createErr, "ft_revoked", true))
	})
}
