package v1

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewEnvelope(t *testing.T) {
	env := NewEnvelope(MessageTypePing, nil)
	require.Equal(t, MessageTypePing, env.Type)
	require.Nil(t, env.Payload)

	env = NewEnvelope(MessageTypePong, PongPayload{Timestamp: 42})
	require.Equal(t, MessageTypePong, env.Type)
	require.JSONEq(t, `{"timestamp":42}`, string(env.Payload))
}

func TestDecodePayload(t *testing.T) {
	env := NewEnvelope(MessageTypeError, ErrorPayload{Message: "boom"})
	var out ErrorPayload
	require.NoError(t, env.DecodePayload(&out))
	require.Equal(t, "boom", out.Message)

	empty := Envelope{Type: MessageTypePing}
	require.NoError(t, empty.DecodePayload(&out))
}

func TestBuildTunnelUpdatedPayload(t *testing.T) {
	payload := BuildTunnelUpdatedPayload("tid", StatusPaused, "")
	require.Equal(t, "tid", payload.TunnelID)
	require.Equal(t, StatusPaused, payload.Status)
	require.Empty(t, payload.PublicURL)

	payload = BuildTunnelUpdatedPayload("tid", StatusActive, "https://example/t")
	require.Equal(t, "https://example/t", payload.PublicURL)
}

func TestBuildTunnelClosedPayload(t *testing.T) {
	payload := BuildTunnelClosedPayload("tid", "")
	require.Equal(t, ReasonUnknown, payload.Reason)

	payload = BuildTunnelClosedPayload("tid", ReasonDeleted)
	require.Equal(t, ReasonDeleted, payload.Reason)
}

func TestIsTerminalStatus(t *testing.T) {
	require.True(t, IsTerminalStatus(StatusExpired))
	require.False(t, IsTerminalStatus(StatusActive))
}

func TestIsTerminalReason(t *testing.T) {
	for _, reason := range []string{
		ReasonDeleted,
		ReasonDeletedAll,
		ReasonClosedByClient,
		ReasonClientDisconnected,
		ReasonExpired,
	} {
		require.True(t, IsTerminalReason(reason), reason)
	}
	require.False(t, IsTerminalReason(ReasonUnknown))
	require.False(t, IsTerminalReason("other"))
}
