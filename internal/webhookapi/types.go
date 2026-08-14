// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

// Package webhookapi is the Fortunnels webhook-test CI API client used by the
// fortunnels webhook CLI. Tokens are supplied out-of-band and never persisted.
package webhookapi

// TokenCreateRequest is the dashboard token creation payload.
type TokenCreateRequest struct {
	Name         string   `json:"name"`
	EndpointIDs  []string `json:"endpoint_ids"`
	ActionScopes []string `json:"action_scopes"`
	ExpiresDays  int      `json:"expires_days"`
}

// TokenView is the dashboard-safe token projection.
type TokenView struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Prefix       string   `json:"prefix"`
	EndpointIDs  []string `json:"endpoint_ids"`
	ActionScopes []string `json:"action_scopes"`
	ExpiresAt    string   `json:"expires_at"`
	CreatedAt    string   `json:"created_at"`
}

// TokenCreateResponse returns the one-time plaintext value.
type TokenCreateResponse struct {
	Token TokenView `json:"token"`
	Value string    `json:"value"`
}

// SendRequest is the token-authenticated send payload.
type SendRequest struct {
	EndpointID string         `json:"endpoint_id"`
	Request    SendRequestReq `json:"request"`
	Tags       []string       `json:"tags,omitempty"`
	RunID      string         `json:"run_id,omitempty"`
}

// SendRequestReq is the HTTP request descriptor inside a send.
type SendRequestReq struct {
	Method      string            `json:"method"`
	Path        string            `json:"path"`
	RawQuery    string            `json:"query,omitempty"`
	ContentType string            `json:"content_type,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Body        string            `json:"body,omitempty"`
}

// SendResult is the send outcome.
type SendResult struct {
	EventID          string `json:"event_id"`
	Delivered        bool   `json:"delivered"`
	DownstreamStatus *int   `json:"downstream_status,omitempty"`
	Notes            string `json:"notes,omitempty"`
}

// Event is the masked captured event returned by list/wait.
type Event struct {
	ID        string            `json:"id"`
	Timestamp string            `json:"timestamp"`
	TunnelID  string            `json:"tunnel_id"`
	Method    string            `json:"method"`
	Path      string            `json:"path"`
	Status    int               `json:"status"`
	Tags      []string          `json:"tags,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Body      string            `json:"body"`
}

// ListEventsResponse is a page of events plus the opaque next cursor.
type ListEventsResponse struct {
	Events     []Event `json:"events"`
	NextCursor string  `json:"next_cursor,omitempty"`
	HasMore    bool    `json:"has_more"`
}

// WaitRequest is the token-authenticated wait payload.
type WaitRequest struct {
	EndpointID string         `json:"endpoint_id"`
	Cursor     string         `json:"cursor,omitempty"`
	TimeoutMS  int            `json:"timeout_ms"`
	Matcher    map[string]any `json:"matcher,omitempty"`
}

// WaitResponse is the wait outcome; Event is nil when nothing matched.
type WaitResponse struct {
	Event   *Event `json:"event,omitempty"`
	Cursor  string `json:"cursor,omitempty"`
	Matched bool   `json:"matched"`
}

// ClearRequest bounds a test-data deletion.
type ClearRequest struct {
	EndpointID string `json:"endpoint_id"`
	RunID      string `json:"run_id,omitempty"`
	From       string `json:"from,omitempty"`
	To         string `json:"to,omitempty"`
	DryRun     bool   `json:"dry_run,omitempty"`
}

// ClearResponse reports the deletion count.
type ClearResponse struct {
	DryRun bool `json:"dry_run"`
	Count  int  `json:"count"`
}

// MockRule is the Stage 7 mock rule projection.
type MockRule struct {
	ID            string   `json:"id"`
	TunnelID      string   `json:"tunnel_id"`
	Priority      int      `json:"priority"`
	Enabled       bool     `json:"enabled"`
	Method        string   `json:"method"`
	PathMode      string   `json:"path_mode"`
	Path          string   `json:"path"`
	MatchAll      bool     `json:"match_all"`
	Response      MockResp `json:"response"`
	MaxUses       *int     `json:"max_uses,omitempty"`
	RemainingUses *int     `json:"remaining_uses,omitempty"`
	Version       int      `json:"version"`
}

// MockResp is the mock rule response descriptor.
type MockResp struct {
	Status  int          `json:"status"`
	Headers []MockHeader `json:"headers"`
	Body    string       `json:"body"`
	DelayMS int          `json:"delay_ms"`
}

// MockHeader is a mock response header.
type MockHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// MockRuleInput is the create/update payload.
type MockRuleInput struct {
	Priority int      `json:"priority"`
	Enabled  *bool    `json:"enabled,omitempty"`
	Method   string   `json:"method,omitempty"`
	PathMode string   `json:"path_mode,omitempty"`
	Path     string   `json:"path,omitempty"`
	Preset   string   `json:"preset,omitempty"`
	Response MockResp `json:"response"`
	MaxUses  *int     `json:"max_uses,omitempty"`
	Version  int      `json:"version,omitempty"`
}
