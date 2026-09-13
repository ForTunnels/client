package auth

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type accountRequiredCreateError struct{}

func (accountRequiredCreateError) Error() string     { return "safe" }
func (accountRequiredCreateError) ErrorCode() string { return "authentication_required" }
func (accountRequiredCreateError) HTTPStatus() int   { return 401 }

func TestAccountRequiredInvalidBearerHasNoGuestFallback(t *testing.T) {
	t.Parallel()
	err := MapCreateTunnelAuthError(accountRequiredCreateError{}, "bad-token", true, "tcp")
	require.Error(t, err)
	require.Contains(t, err.Error(), "-login and -pass-stdin")
	require.NotContains(t, err.Error(), "guest mode")
	require.NotContains(t, err.Error(), "create a token")
	require.False(t, errors.Is(err, accountRequiredCreateError{}))
}
