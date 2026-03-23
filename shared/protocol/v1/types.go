package v1

import (
	"encoding/json"
	"fmt"
	"time"
)

const (
	StatusActive    = "active"
	StatusPaused    = "paused"
	StatusNotActive = "not active"
	StatusExpired   = "expired"
)

const (
	EventTunnelUpdated = "tunnel_updated"
	EventTunnelClosed  = "tunnel_closed"
)

const (
	ReasonDeleted            = "deleted"
	ReasonDeletedAll         = "deleted_all"
	ReasonClosedByClient     = "closed_by_client"
	ReasonClientDisconnected = "client_disconnected"
	ReasonExpired            = "expired"
	ReasonUnknown            = "unknown"
)

const (
	MessageTypeCreateTunnel  = "create_tunnel"
	MessageTypeTunnelCreated = "tunnel_created"
	MessageTypePing          = "ping"
	MessageTypePong          = "pong"
	MessageTypeCloseTunnel   = "close_tunnel"
	MessageTypeSubscribe     = "subscribe"
	MessageTypeSubscribed    = "subscribed"
	MessageTypeError         = "error"
)

type Envelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

func NewEnvelope(msgType string, payload any) Envelope {
	if payload == nil {
		return Envelope{Type: msgType}
	}
	b, err := json.Marshal(payload)
	if err != nil {
		panic(fmt.Sprintf("protocol/v1: marshal payload for %s: %v", msgType, err))
	}
	return Envelope{Type: msgType, Payload: b}
}

func (e Envelope) DecodePayload(target any) error {
	if len(e.Payload) == 0 {
		return nil
	}
	return json.Unmarshal(e.Payload, target)
}

type Tunnel struct {
	ID                    string    `json:"id"`
	UserID                int64     `json:"user_id,omitempty"`
	OwnerLogin            string    `json:"owner_login,omitempty"`
	Protocol              string    `json:"protocol"`
	TargetAddr            string    `json:"target_addr"`
	PublicURL             string    `json:"public_url"`
	Status                string    `json:"status"`
	CreatedAt             time.Time `json:"created_at"`
	LastActive            time.Time `json:"last_active"`
	Connections           int       `json:"connections"`
	RedirectHTTP          bool      `json:"redirect_http"`
	Transport             string    `json:"transport,omitempty"`
	TLSInsecureSkipVerify bool      `json:"tls_insecure_skip_verify,omitempty"`
	TLSServerName         string    `json:"tls_server_name,omitempty"`
	Reachable             bool      `json:"reachable"`
	ReachCheckAt          time.Time `json:"reach_check_at"`
	BytesUsed             int64     `json:"bytes_used"`
	ExpiresAt             time.Time `json:"expires_at"`
	TrafficLimitBytes     int64     `json:"traffic_limit_bytes"`
	IsGuest               bool      `json:"is_guest,omitempty"`
}

type TunnelCreateRequest struct {
	Protocol              string `json:"protocol"`
	TargetAddr            string `json:"target_addr"`
	UserID                string `json:"user_id"`
	RedirectHTTP          *bool  `json:"redirect_http,omitempty"`
	TLSInsecureSkipVerify *bool  `json:"tls_insecure_skip_verify,omitempty"`
	TLSServerName         string `json:"tls_server_name,omitempty"`
}

type TunnelPatchRequest struct {
	ID           string `json:"id"`
	Action       string `json:"action"`
	RedirectHTTP *bool  `json:"redirect_http,omitempty"`
	Transport    string `json:"transport,omitempty"`
}

// TunnelListResponse is returned for tunnel list and existence checks.
// Count is the number of tunnels in this JSON payload (current page size).
// Total is the total row count for the list scope (admin global, user-owned, or guest); set for all paginated GET /api/tunnels lists.
type TunnelListResponse struct {
	Exists  bool     `json:"exists,omitempty"`
	Status  string   `json:"status,omitempty"`
	Tunnels []Tunnel `json:"tunnels"`
	Count   int      `json:"count"`
	Total   int64    `json:"total"`
}

type DomainBindingRequest struct {
	TunnelID string `json:"tunnel_id"`
	Host     string `json:"host"`
}

type DomainBindingResponse struct {
	Status string `json:"status"`
}

type DomainListResponse struct {
	Domains map[string]string `json:"domains"`
	Count   int               `json:"count"`
}

type CreateTunnelPayload = TunnelCreateRequest

type CloseTunnelPayload struct {
	TunnelID string `json:"tunnel_id"`
}

type SubscribePayload struct {
	TunnelID string `json:"tunnel_id"`
}

type TunnelCreatedPayload struct {
	Tunnel Tunnel `json:"tunnel"`
}

type PongPayload struct {
	Timestamp int64 `json:"timestamp,omitempty"`
}

type ErrorPayload struct {
	Message string `json:"message"`
}

type LifecycleEventPayload struct {
	TunnelID  string `json:"tunnel_id"`
	Status    string `json:"status,omitempty"`
	PublicURL string `json:"public_url,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

func BuildTunnelUpdatedPayload(tunnelID, status, publicURL string) LifecycleEventPayload {
	payload := LifecycleEventPayload{TunnelID: tunnelID, Status: status}
	if publicURL != "" {
		payload.PublicURL = publicURL
	}
	return payload
}

func BuildTunnelClosedPayload(tunnelID, reason string) LifecycleEventPayload {
	if reason == "" {
		reason = ReasonUnknown
	}
	return LifecycleEventPayload{TunnelID: tunnelID, Reason: reason}
}

func IsTerminalStatus(status string) bool {
	return status == StatusExpired
}

func IsTerminalReason(reason string) bool {
	switch reason {
	case ReasonDeleted, ReasonDeletedAll, ReasonClosedByClient, ReasonClientDisconnected, ReasonExpired:
		return true
	default:
		return false
	}
}
