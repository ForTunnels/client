// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package auth

import (
	"encoding/base64"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsBearerTokenExpiredOrMalformed(t *testing.T) {
	t.Parallel()

	expiredPayload := base64.RawURLEncoding.EncodeToString([]byte(`{"exp":1}`))
	expiredJWT := "eyJhbGciOiJIUzI1NiJ9." + expiredPayload + ".sig"

	futureExp := time.Now().Add(time.Hour).Unix()
	validPayload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, futureExp)))
	validJWT := "eyJhbGciOiJIUzI1NiJ9." + validPayload + ".sig"

	assert.True(t, isBearerTokenExpiredOrMalformed("stale-invalid-token"))
	assert.True(t, isBearerTokenExpiredOrMalformed(expiredJWT))
	assert.False(t, isBearerTokenExpiredOrMalformed(validJWT))
	assert.False(t, isBearerTokenExpiredOrMalformed("ft_live_token_abc"))
}

func TestValidateConfigAuthtoken(t *testing.T) {
	t.Parallel()

	t.Run("not from config file", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, validateConfigAuthtoken("stale-invalid-token", false))
	})

	t.Run("empty token", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, validateConfigAuthtoken("", true))
	})

	t.Run("valid ft token", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, validateConfigAuthtoken("ft_live_token_abc", true))
	})

	t.Run("malformed jwt from config", func(t *testing.T) {
		t.Parallel()
		err := validateConfigAuthtoken("stale-invalid-token", true)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "fortunnels.yml authtoken is expired or invalid")
		assert.Contains(t, err.Error(), "config add-authtoken")
	})
}
