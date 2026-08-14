// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package webhookapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultBaseURL is the production API base.
const DefaultBaseURL = "https://fortunnels.ru"

// apiBase is the fixed webhook-test API path prefix.
const apiBase = "/api/v1/webhook-test"

// Client talks to the webhook-test CI API with a bearer token. The token is
// held only in memory and is never printed or persisted.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// New builds a client. baseURL may be empty to use DefaultBaseURL.
func New(baseURL, token string) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		baseURL: baseURL,
		token:   strings.TrimSpace(token),
		http:    &http.Client{Timeout: 35 * time.Second},
	}
}

// Error is a stable machine error from the API. Status >= 500 or 0 means an
// internal/transport failure; the caller maps these to exit code 2.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("api error (%s): %s", e.Code, e.Message)
	}
	return fmt.Sprintf("api error: HTTP %d", e.Status)
}

// IsTimeout reports whether err is a wait/assert timeout.
func IsTimeout(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *Error
	if !asError(err, &apiErr) {
		return false
	}
	return apiErr.Code == "wait_timeout"
}

// IsAuth reports whether err is an authentication/authorization failure.
func IsAuth(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *Error
	if !asError(err, &apiErr) {
		return false
	}
	switch apiErr.Code {
	case "invalid_token", "token_expired", "token_revoked", "invalid_scope", "foreign_scope", "unauthorized":
		return true
	}
	return apiErr.Status == http.StatusUnauthorized || apiErr.Status == http.StatusForbidden
}

func asError(err error, target **Error) bool {
	return errors.As(err, target)
}

// Send dispatches one test event.
func (c *Client) Send(ctx context.Context, req *SendRequest) (SendResult, error) {
	var out SendResult
	err := c.doJSON(ctx, http.MethodPost, apiBase+"/events/send", nil, req, &out)
	return out, err
}

// ListEvents returns a page of events after an optional cursor.
func (c *Client) ListEvents(ctx context.Context, endpointID, cursor string, limit int) (ListEventsResponse, error) {
	q := url.Values{}
	q.Set("endpoint_id", endpointID)
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	var out ListEventsResponse
	err := c.doJSON(ctx, http.MethodGet, apiBase+"/events", q, nil, &out)
	return out, err
}

// Wait blocks until a matching event arrives, the timeout expires, or the
// context is canceled. Returns ErrTimeout-style Error on wait_timeout.
func (c *Client) Wait(ctx context.Context, req WaitRequest) (WaitResponse, error) {
	var out WaitResponse
	err := c.doJSON(ctx, http.MethodPost, apiBase+"/events/wait", nil, req, &out)
	return out, err
}

// Clear deletes (or dry-run counts) test-generated events in scope.
func (c *Client) Clear(ctx context.Context, req ClearRequest) (ClearResponse, error) {
	var out ClearResponse
	err := c.doJSON(ctx, http.MethodPost, apiBase+"/events/clear", nil, req, &out)
	return out, err
}

// ListMock returns the endpoint's mock rules.
func (c *Client) ListMock(ctx context.Context, endpointID string) ([]MockRule, error) {
	q := url.Values{}
	q.Set("endpoint_id", endpointID)
	var out struct {
		Rules []MockRule `json:"rules"`
	}
	err := c.doJSON(ctx, http.MethodGet, apiBase+"/mock/rules", q, nil, &out)
	return out.Rules, err
}

// CreateMock creates one mock rule.
func (c *Client) CreateMock(ctx context.Context, endpointID string, in *MockRuleInput) (MockRule, error) {
	var out struct {
		Rule MockRule `json:"rule"`
	}
	body := map[string]any{
		"endpoint_id": endpointID,
		"priority":    in.Priority,
		"method":      in.Method,
		"path_mode":   in.PathMode,
		"path":        in.Path,
		"preset":      in.Preset,
		"response":    in.Response,
		"max_uses":    in.MaxUses,
		"enabled":     in.Enabled,
	}
	err := c.doJSON(ctx, http.MethodPost, apiBase+"/mock/rules", nil, body, &out)
	return out.Rule, err
}

// UpdateMock updates one mock rule.
func (c *Client) UpdateMock(ctx context.Context, endpointID, ruleID string, in *MockRuleInput) (MockRule, error) {
	var out struct {
		Rule MockRule `json:"rule"`
	}
	body := map[string]any{
		"endpoint_id": endpointID,
		"priority":    in.Priority,
		"method":      in.Method,
		"path_mode":   in.PathMode,
		"path":        in.Path,
		"preset":      in.Preset,
		"response":    in.Response,
		"max_uses":    in.MaxUses,
		"version":     in.Version,
		"enabled":     in.Enabled,
	}
	err := c.doJSON(ctx, http.MethodPatch, apiBase+"/mock/rules/"+url.PathEscape(ruleID), nil, body, &out)
	return out.Rule, err
}

// DeleteMock removes one mock rule.
func (c *Client) DeleteMock(ctx context.Context, endpointID, ruleID string) error {
	q := url.Values{}
	q.Set("endpoint_id", endpointID)
	return c.doJSON(ctx, http.MethodDelete, apiBase+"/mock/rules/"+url.PathEscape(ruleID), q, nil, nil)
}

func (c *Client) doJSON(ctx context.Context, method, path string, q url.Values, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if q != nil {
		req.URL.RawQuery = q.Encode()
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		apiErr := &Error{Status: resp.StatusCode}
		if err := json.Unmarshal(raw, apiErr); err != nil {
			apiErr.Code = "http_error"
			apiErr.Message = http.StatusText(resp.StatusCode)
		}
		if apiErr.Code == "" {
			apiErr.Code = "http_error"
			apiErr.Message = http.StatusText(resp.StatusCode)
		}
		return apiErr
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
