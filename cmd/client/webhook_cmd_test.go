// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

// webhookTestServer mimics the /api/v1/webhook-test surface.
type webhookTestServer struct {
	mu      sync.Mutex
	events  []map[string]any
	rules   []map[string]any
	updated map[string]any
	updates int
	created int
	cleared int
	dryRun  bool
	authErr string
	timeout bool
}

func (s *webhookTestServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/webhook-test/events/send", func(w http.ResponseWriter, r *http.Request) {
		if s.checkAuth(w, r) {
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.mu.Lock()
		ev := map[string]any{"event_id": "ev-1", "delivered": true}
		s.events = append(s.events, ev)
		s.mu.Unlock()
		writeJSON(w, 200, ev)
	})
	mux.HandleFunc("/api/v1/webhook-test/events", func(w http.ResponseWriter, r *http.Request) {
		if s.checkAuth(w, r) {
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		page := map[string]any{"events": s.events, "has_more": false}
		writeJSON(w, 200, page)
	})
	mux.HandleFunc("/api/v1/webhook-test/events/wait", func(w http.ResponseWriter, r *http.Request) {
		if s.checkAuth(w, r) {
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.timeout {
			writeJSON(w, 408, map[string]any{"code": "wait_timeout", "message": "no matching event"})
			return
		}
		if len(s.events) == 0 {
			writeJSON(w, 408, map[string]any{"code": "wait_timeout", "message": "no matching event"})
			return
		}
		event := map[string]any{
			"id": "ev-1", "timestamp": "2026-01-01T00:00:00Z",
			"method": "POST", "path": "/h", "status": 201,
		}
		writeJSON(w, 200, map[string]any{"event": event, "matched": true, "cursor": "cur-2"})
	})
	mux.HandleFunc("/api/v1/webhook-test/events/clear", func(w http.ResponseWriter, r *http.Request) {
		if s.checkAuth(w, r) {
			return
		}
		var body struct {
			DryRun bool `json:"dry_run"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.mu.Lock()
		s.cleared++
		s.dryRun = body.DryRun
		s.mu.Unlock()
		writeJSON(w, 200, map[string]any{"dry_run": body.DryRun, "count": 3})
	})
	mux.HandleFunc("/api/v1/webhook-test/mock/rules", func(w http.ResponseWriter, r *http.Request) {
		if s.checkAuth(w, r) {
			return
		}
		switch r.Method {
		case http.MethodGet:
			s.mu.Lock()
			writeJSON(w, 200, map[string]any{"rules": s.rules})
			s.mu.Unlock()
		case http.MethodPost:
			s.mu.Lock()
			s.created++
			rule := map[string]any{"id": "rule-1", "version": 1}
			s.rules = append(s.rules, rule)
			s.mu.Unlock()
			writeJSON(w, 201, map[string]any{"rule": rule})
		}
	})
	mux.HandleFunc("/api/v1/webhook-test/mock/rules/", func(w http.ResponseWriter, r *http.Request) {
		if s.checkAuth(w, r) {
			return
		}
		if r.Method == http.MethodDelete {
			writeJSON(w, 200, map[string]any{"deleted": true})
			return
		}
		var input map[string]any
		_ = json.NewDecoder(r.Body).Decode(&input)
		s.mu.Lock()
		s.updated = input
		s.updates++
		s.mu.Unlock()
		version := 1
		if current, ok := input["version"].(float64); ok {
			version = int(current) + 1
		}
		ruleID := strings.TrimPrefix(r.URL.Path, "/api/v1/webhook-test/mock/rules/")
		writeJSON(w, 200, map[string]any{"rule": map[string]any{"id": ruleID, "version": version}})
	})
	return mux
}

func (s *webhookTestServer) checkAuth(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Authorization") != "Bearer test-token" {
		s.mu.Lock()
		code := s.authErr
		s.mu.Unlock()
		if code == "" {
			code = "invalid_token"
		}
		writeJSON(w, 401, map[string]any{"code": code, "message": "invalid token"})
		return true
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func newWebhookTestEnv(t *testing.T, server *webhookTestServer) string {
	t.Helper()
	ts := httptest.NewServer(server.handler())
	t.Cleanup(ts.Close)
	return ts.URL
}

func captureCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errBuf strings.Builder
	oldOut, oldErr := os.Stdout, os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout, os.Stderr = wOut, wErr
	exit := runWebhookCommand(args)
	os.Stdout, os.Stderr = oldOut, oldErr
	wOut.Close()
	wErr.Close()
	oBytes, _ := io.ReadAll(rOut)
	eBytes, _ := io.ReadAll(rErr)
	out.Write(oBytes)
	errBuf.Write(eBytes)
	return exit, out.String(), errBuf.String()
}

func TestWebhookSendSuccess(t *testing.T) {
	url := newWebhookTestEnv(t, &webhookTestServer{})
	code, out, _ := captureCLI(t, "send", "--api-url", url, "--api-token", "test-token",
		"--endpoint", "ep-1", "--path", "/hook", "--body", "{}")
	if code != exitOK {
		t.Fatalf("exit=%d want %d", code, exitOK)
	}
	if !strings.Contains(out, "event_id=ev-1") || !strings.Contains(out, "delivered=true") {
		t.Fatalf("unexpected output: %q", out)
	}
	if strings.Contains(out, "test-token") {
		t.Fatalf("token leaked in output: %q", out)
	}
}

func TestWebhookSendAuthErrorExit2(t *testing.T) {
	url := newWebhookTestEnv(t, &webhookTestServer{})
	code, out, errOut := captureCLI(t, "send", "--api-url", url, "--api-token", "wrong-token",
		"--endpoint", "ep-1")
	if code != exitError {
		t.Fatalf("exit=%d want %d", code, exitError)
	}
	if !strings.Contains(errOut, "invalid_token") {
		t.Fatalf("unexpected stderr: %q", errOut)
	}
	if strings.Contains(out+errOut, "wrong-token") {
		t.Fatalf("token leaked: %q", out+errOut)
	}
}

func TestWebhookEventsList(t *testing.T) {
	server := &webhookTestServer{}
	server.events = []map[string]any{
		{"id": "ev-1", "timestamp": "2026-01-01T00:00:00Z", "method": "POST", "path": "/h", "status": 200},
	}
	url := newWebhookTestEnv(t, server)
	code, out, _ := captureCLI(t, "events", "list", "--api-url", url, "--api-token", "test-token", "--endpoint", "ep-1")
	if code != exitOK {
		t.Fatalf("exit=%d want %d", code, exitOK)
	}
	if !strings.Contains(out, "total=1") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestWebhookEventsListUnknownSubcommand(t *testing.T) {
	code, _, _ := captureCLI(t, "events", "bogus")
	if code != exitError {
		t.Fatalf("exit=%d want %d", code, exitError)
	}
}

func TestWebhookWaitMatch(t *testing.T) {
	server := &webhookTestServer{}
	server.events = []map[string]any{{"id": "ev-1", "method": "POST", "path": "/h", "status": 200}}
	url := newWebhookTestEnv(t, server)
	code, out, _ := captureCLI(t, "wait", "--api-url", url, "--api-token", "test-token",
		"--endpoint", "ep-1", "--timeout", "1000")
	if code != exitOK {
		t.Fatalf("exit=%d want %d", code, exitOK)
	}
	if !strings.Contains(out, "matched event_id=ev-1") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestWebhookWaitTimeoutExit1(t *testing.T) {
	server := &webhookTestServer{timeout: true}
	url := newWebhookTestEnv(t, server)
	code, _, errOut := captureCLI(t, "wait", "--api-url", url, "--api-token", "test-token",
		"--endpoint", "ep-1", "--timeout", "500")
	if code != exitFail {
		t.Fatalf("exit=%d want %d", code, exitFail)
	}
	if !strings.Contains(errOut, "no matching event") {
		t.Fatalf("unexpected stderr: %q", errOut)
	}
}

func TestWebhookAssertStatusMismatchExit1(t *testing.T) {
	server := &webhookTestServer{}
	server.events = []map[string]any{{"id": "ev-1", "method": "POST", "path": "/h", "status": 200}}
	url := newWebhookTestEnv(t, server)
	code, _, errOut := captureCLI(t, "assert", "--api-url", url, "--api-token", "test-token",
		"--endpoint", "ep-1", "--assert-status", "500", "--timeout", "1000")
	if code != exitFail {
		t.Fatalf("exit=%d want %d", code, exitFail)
	}
	if !strings.Contains(errOut, "assertion failed") {
		t.Fatalf("unexpected stderr: %q", errOut)
	}
}

func TestWebhookAssertStatusMatch(t *testing.T) {
	server := &webhookTestServer{}
	server.events = []map[string]any{{"id": "ev-1", "method": "POST", "path": "/h", "status": 201}}
	url := newWebhookTestEnv(t, server)
	code, out, _ := captureCLI(t, "assert", "--api-url", url, "--api-token", "test-token",
		"--endpoint", "ep-1", "--assert-status", "201", "--timeout", "1000")
	if code != exitOK {
		t.Fatalf("exit=%d want %d", code, exitOK)
	}
	if !strings.Contains(out, "matched event_id=ev-1") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestWebhookClearDryRun(t *testing.T) {
	url := newWebhookTestEnv(t, &webhookTestServer{})
	code, out, _ := captureCLI(t, "clear", "--api-url", url, "--api-token", "test-token",
		"--endpoint", "ep-1", "--dry-run")
	if code != exitOK {
		t.Fatalf("exit=%d want %d", code, exitOK)
	}
	if !strings.Contains(out, "dry-run: 3") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestWebhookMockCreateAndList(t *testing.T) {
	url := newWebhookTestEnv(t, &webhookTestServer{})
	code, out, _ := captureCLI(t, "mock", "create", "--api-url", url, "--api-token", "test-token",
		"--endpoint", "ep-1", "--status", "204")
	if code != exitOK {
		t.Fatalf("create exit=%d want %d", code, exitOK)
	}
	if !strings.Contains(out, "created rule=rule-1") {
		t.Fatalf("unexpected create output: %q", out)
	}
	code, out, _ = captureCLI(t, "mock", "list", "--api-url", url, "--api-token", "test-token", "--endpoint", "ep-1")
	if code != exitOK {
		t.Fatalf("list exit=%d want %d", code, exitOK)
	}
	if !strings.Contains(out, "total=1") {
		t.Fatalf("unexpected list output: %q", out)
	}
}

func TestWebhookMockUpdateAndDelete(t *testing.T) {
	url := newWebhookTestEnv(t, &webhookTestServer{rules: []map[string]any{{"id": "rule-1", "version": 1}}})
	code, out, _ := captureCLI(t, "mock", "update", "--api-url", url, "--api-token", "test-token",
		"--endpoint", "ep-1", "--rule", "rule-1", "--status", "202", "--version", "1")
	if code != exitOK {
		t.Fatalf("update exit=%d want %d", code, exitOK)
	}
	if !strings.Contains(out, "updated rule=rule-1 version=2") {
		t.Fatalf("unexpected update output: %q", out)
	}
	code, out, _ = captureCLI(t, "mock", "delete", "--api-url", url, "--api-token", "test-token",
		"--endpoint", "ep-1", "--rule", "rule-1")
	if code != exitOK {
		t.Fatalf("delete exit=%d want %d", code, exitOK)
	}
	if !strings.Contains(out, "deleted rule=rule-1") {
		t.Fatalf("unexpected delete output: %q", out)
	}
}

func TestWebhookMockUpdatePreservesUnspecifiedFields(t *testing.T) {
	server := &webhookTestServer{rules: []map[string]any{{
		"id":        "rule-1",
		"priority":  17,
		"enabled":   false,
		"method":    "POST",
		"path_mode": "prefix",
		"path":      "/original",
		"response": map[string]any{
			"status":   418,
			"headers":  []map[string]any{{"name": "X-Preserved", "value": "yes"}},
			"body":     "original response",
			"delay_ms": 125,
		},
		"max_uses": 3,
		"version":  5,
	}}}
	url := newWebhookTestEnv(t, server)
	code, _, errOut := captureCLI(t, "mock", "update", "--api-url", url, "--api-token", "test-token",
		"--endpoint", "ep-1", "--rule", "rule-1", "--max-uses", "13")
	if code != exitOK {
		t.Fatalf("update exit=%d want %d, stderr=%q", code, exitOK, errOut)
	}

	server.mu.Lock()
	updated := server.updated
	server.mu.Unlock()
	if updated == nil {
		t.Fatal("update request was not recorded")
	}
	for field, want := range map[string]any{
		"priority":  float64(17),
		"enabled":   false,
		"method":    "POST",
		"path_mode": "prefix",
		"path":      "/original",
		"max_uses":  float64(13),
		"version":   float64(5),
	} {
		if got := updated[field]; got != want {
			t.Errorf("update field %q = %#v, want %#v", field, got, want)
		}
	}
	response, ok := updated["response"].(map[string]any)
	if !ok {
		t.Fatalf("update response = %#v, want object", updated["response"])
	}
	for field, want := range map[string]any{
		"status":   float64(418),
		"body":     "original response",
		"delay_ms": float64(125),
	} {
		if got := response[field]; got != want {
			t.Errorf("response field %q = %#v, want %#v", field, got, want)
		}
	}
	headers, ok := response["headers"].([]any)
	if !ok || len(headers) != 1 {
		t.Fatalf("response headers = %#v, want one header", response["headers"])
	}
	header, ok := headers[0].(map[string]any)
	if !ok || header["name"] != "X-Preserved" || header["value"] != "yes" {
		t.Errorf("response header = %#v, want X-Preserved: yes", headers[0])
	}
}

func TestWebhookMockUpdateUnknownRuleExit2(t *testing.T) {
	server := &webhookTestServer{}
	url := newWebhookTestEnv(t, server)
	code, _, errOut := captureCLI(t, "mock", "update", "--api-url", url, "--api-token", "test-token",
		"--endpoint", "ep-1", "--rule", "missing", "--max-uses", "13")
	if code != exitError {
		t.Fatalf("update exit=%d want %d", code, exitError)
	}
	if !strings.Contains(errOut, "rule not found") {
		t.Fatalf("unexpected stderr: %q", errOut)
	}
	server.mu.Lock()
	updates := server.updates
	server.mu.Unlock()
	if updates != 0 {
		t.Fatalf("update requests = %d, want 0", updates)
	}
}

func TestWebhookMissingTokenExit2(t *testing.T) {
	url := newWebhookTestEnv(t, &webhookTestServer{})
	t.Setenv(envWebhookToken, "")
	code, _, errOut := captureCLI(t, "send", "--api-url", url, "--endpoint", "ep-1")
	if code != exitError {
		t.Fatalf("exit=%d want %d", code, exitError)
	}
	if !strings.Contains(errOut, "no API token") {
		t.Fatalf("unexpected stderr: %q", errOut)
	}
}

func TestWebhookTokenFromEnv(t *testing.T) {
	url := newWebhookTestEnv(t, &webhookTestServer{})
	t.Setenv(envWebhookToken, "test-token")
	code, out, _ := captureCLI(t, "send", "--api-url", url, "--endpoint", "ep-1")
	if code != exitOK {
		t.Fatalf("exit=%d want %d", code, exitOK)
	}
	if !strings.Contains(out, "event_id=ev-1") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestWebhookUnknownCommandExit2(t *testing.T) {
	code, _, _ := captureCLI(t, "bogus")
	if code != exitError {
		t.Fatalf("exit=%d want %d", code, exitError)
	}
}

func TestWebhookHelp(t *testing.T) {
	code, out, _ := captureCLI(t, "help")
	if code != exitOK {
		t.Fatalf("exit=%d want %d", code, exitOK)
	}
	if !strings.Contains(out, "fortunnels webhook") {
		t.Fatalf("unexpected help output: %q", out)
	}
}

func TestWebhookInvalidHeaderMatcherExit2(t *testing.T) {
	url := newWebhookTestEnv(t, &webhookTestServer{})
	code, _, _ := captureCLI(t, "wait", "--api-url", url, "--api-token", "test-token",
		"--endpoint", "ep-1", "--header", "no-colon")
	if code != exitError {
		t.Fatalf("exit=%d want %d", code, exitError)
	}
}

func TestWebhookBodyPointerValueMismatch(t *testing.T) {
	url := newWebhookTestEnv(t, &webhookTestServer{})
	code, _, errOut := captureCLI(t, "wait", "--api-url", url, "--api-token", "test-token",
		"--endpoint", "ep-1", "--body-pointer", "/ok", "--body-value", "true", "--body-value", "false")
	if code != exitError {
		t.Fatalf("exit=%d want %d", code, exitError)
	}
	if !strings.Contains(errOut, "must be paired") {
		t.Fatalf("unexpected stderr: %q", errOut)
	}
}

func TestWebhookSendUnreachableExit2(t *testing.T) {
	code, _, errOut := captureCLI(t, "send", "--api-url", "http://127.0.0.1:1", "--api-token", "t",
		"--endpoint", "ep-1")
	if code != exitError {
		t.Fatalf("exit=%d want %d", code, exitError)
	}
	if !strings.Contains(errOut, "request failed") {
		t.Fatalf("unexpected stderr: %q", errOut)
	}
}
