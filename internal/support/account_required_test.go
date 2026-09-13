package support

import (
	"testing"

	"github.com/stretchr/testify/require"
)

type accountRequiredError struct{}

func (accountRequiredError) Error() string     { return "raw body must not appear" }
func (accountRequiredError) ErrorCode() string { return "authentication_required" }

func TestAccountRequiredTunnelCreationMessageUsesRealFlags(t *testing.T) {
	t.Parallel()
	err := HandleTunnelCreationError(accountRequiredError{}, "https://example.test/base\x1b[31m", "tcp", "localhost:25565")
	expected := "TCP tunnels require a signed-in account. A Free account is sufficient. " +
		"Sign in with -login and -pass-stdin, then run this command again. " +
		"Anonymous mode is available for HTTP/HTTPS only.\n" +
		"   Example: fortunnels tcp 25565 -login YOUR_LOGIN -pass-stdin\n" +
		"   Sign in or register: https://example.test/login"
	require.EqualError(t, err, expected)
	require.NotContains(t, err.Error(), "raw body")
	require.NotContains(t, err.Error(), "\x1b")
}

func TestAccountRequiredUDPTunnelCreationMessageUsesTargetPort(t *testing.T) {
	t.Parallel()
	err := HandleTunnelCreationError(accountRequiredError{}, "https://example.test", "udp", "127.0.0.1:5353")
	require.Contains(t, err.Error(), "fortunnels udp 5353 -login YOUR_LOGIN -pass-stdin")
}
