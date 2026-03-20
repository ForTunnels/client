package v1

import (
	"encoding/json"
	"testing"
	"time"
)

func TestEnvelopeRoundTrip(t *testing.T) {
	t.Parallel()

	createdAt := time.Unix(1_700_000_000, 0).UTC()
	payload := TunnelCreatedPayload{
		Tunnel: Tunnel{
			ID:         "tid-1",
			UserID:     42,
			Protocol:   "http",
			TargetAddr: "localhost:8080",
			PublicURL:  "https://tid-1.example.com",
			Status:     StatusActive,
			CreatedAt:  createdAt,
			LastActive: createdAt,
			IsGuest:    true,
		},
	}

	data, err := json.Marshal(NewEnvelope(MessageTypeTunnelCreated, payload))
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}

	var envelope Envelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}

	if envelope.Type != MessageTypeTunnelCreated {
		t.Fatalf("unexpected type %q", envelope.Type)
	}

	var decoded TunnelCreatedPayload
	if err := envelope.DecodePayload(&decoded); err != nil {
		t.Fatalf("decode payload: %v", err)
	}

	if decoded.Tunnel.ID != payload.Tunnel.ID {
		t.Fatalf("unexpected tunnel id %q", decoded.Tunnel.ID)
	}
	if decoded.Tunnel.PublicURL != payload.Tunnel.PublicURL {
		t.Fatalf("unexpected public_url %q", decoded.Tunnel.PublicURL)
	}
}

func TestTunnelListResponseJSON(t *testing.T) {
	t.Parallel()

	response := TunnelListResponse{
		Exists: true,
		Status: StatusPaused,
		Tunnels: []Tunnel{
			{
				ID:         "tid-2",
				Protocol:   "https",
				TargetAddr: "localhost:8443",
				PublicURL:  "https://tid-2.example.com",
				Status:     StatusPaused,
			},
		},
		Count: 1,
		Total: 42,
	}

	data, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	var decoded TunnelListResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if !decoded.Exists || decoded.Count != 1 || decoded.Total != 42 {
		t.Fatalf("unexpected existence payload: %+v", decoded)
	}
	if decoded.Tunnels[0].Status != StatusPaused {
		t.Fatalf("unexpected tunnel status %q", decoded.Tunnels[0].Status)
	}
}
