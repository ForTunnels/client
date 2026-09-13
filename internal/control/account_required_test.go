package control

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountRequiredCreateErrorPrefersStructuredSafeMessage(t *testing.T) {
	t.Parallel()
	err := decodeTunnelCreateError(401, []byte(`{"error":"legacy","code":"authentication_required","message":"sign in\u001b[31m"}`))
	var createErr *TunnelCreateError
	require.True(t, errors.As(err, &createErr))
	require.Equal(t, "authentication_required", createErr.ErrorCode())
	require.Equal(t, 401, createErr.HTTPStatus())
	require.NotContains(t, createErr.Error(), "\x1b")
	require.NotContains(t, createErr.Error(), "legacy")

	oversized := []byte(strings.Repeat("x", maxTunnelErrorBodyBytes+100))
	err = decodeTunnelCreateError(500, oversized)
	require.LessOrEqual(t, len(err.Error()), maxTunnelErrorBodyRunes+40)
}
