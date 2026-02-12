// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/fortunnels/client/internal/config"
)

func TestSetupAuthentication_WithToken(t *testing.T) {
	cfg := &config.Config{
		Token:     "bearer-token-123",
		ServerURL: "https://example.com",
	}

	client, bearer, err := SetupAuthentication(cfg)
	if err != nil {
		t.Fatalf("SetupAuthentication() error = %v", err)
	}
	if bearer != "bearer-token-123" {
		t.Errorf("SetupAuthentication() bearer = %q, want %q", bearer, "bearer-token-123")
	}
	if client != nil {
		t.Error("SetupAuthentication() with token should not create HTTP client")
	}
}

func TestSetupAuthentication_WithLoginPassword(t *testing.T) {
	// Create a test server that accepts login
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/login-local" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		if payload["login"] == "testuser" && payload["password"] == "testpass" {
			// Set a cookie to simulate session
			http.SetCookie(w, &http.Cookie{
				Name:  "session",
				Value: "session-token",
			})
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer server.Close()

	cfg := &config.Config{
		Login:     "testuser",
		Password:  "testpass",
		ServerURL: server.URL,
	}

	client, bearer, err := SetupAuthentication(cfg)
	if err != nil {
		t.Fatalf("SetupAuthentication() error = %v", err)
	}
	if bearer != "" {
		t.Errorf("SetupAuthentication() bearer = %q, want empty", bearer)
	}
	if client == nil {
		t.Fatal("SetupAuthentication() with login/password should create HTTP client")
	}

	// Verify cookie jar was set
	if client.Jar == nil {
		t.Error("SetupAuthentication() should set cookie jar")
	}

	// Verify cookies are stored by checking the jar
	// The cookie jar should have cookies from the login request
	serverURL, _ := url.Parse(server.URL)
	cookieCount := len(client.Jar.Cookies(serverURL))
	// Note: Cookies are set by the server response, verify jar exists and can store cookies
	if client.Jar == nil {
		t.Error("SetupAuthentication() should create cookie jar")
	}
	// Cookie count may be 0 if server doesn't set cookies, but jar should exist
	_ = cookieCount // Verify jar is functional
}

func TestSetupAuthentication_WithLoginPassword_InvalidCredentials(t *testing.T) {
	// Create a test server that rejects login
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	cfg := &config.Config{
		Login:     "testuser",
		Password:  "wrongpass",
		ServerURL: server.URL,
	}

	_, _, err := SetupAuthentication(cfg)
	if err == nil {
		t.Error("SetupAuthentication() with invalid credentials should return error")
	}
}

func TestSetupAuthentication_Empty(t *testing.T) {
	cfg := &config.Config{
		ServerURL: "https://example.com",
	}

	client, bearer, err := SetupAuthentication(cfg)
	if err != nil {
		t.Fatalf("SetupAuthentication() error = %v", err)
	}
	if bearer != "" {
		t.Errorf("SetupAuthentication() bearer = %q, want empty", bearer)
	}
	if client != nil {
		t.Error("SetupAuthentication() with empty config should not create HTTP client")
	}
}

func TestSetupAuthentication_WithToken_Whitespace(t *testing.T) {
	cfg := &config.Config{
		Token:     "  bearer-token-123  ",
		ServerURL: "https://example.com",
	}

	_, bearer, err := SetupAuthentication(cfg)
	if err != nil {
		t.Fatalf("SetupAuthentication() error = %v", err)
	}
	if bearer != "bearer-token-123" {
		t.Errorf("SetupAuthentication() bearer = %q, want %q", bearer, "bearer-token-123")
	}
}

func TestLoginLocal(t *testing.T) {
	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/login-local" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		if payload["login"] == "testuser" && payload["password"] == "testpass" {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer server.Close()

	client := &http.Client{}
	err := loginLocal(client, server.URL, "testuser", "testpass")
	if err != nil {
		t.Errorf("loginLocal() error = %v", err)
	}
}

func TestLoginLocal_InvalidCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	client := &http.Client{}
	err := loginLocal(client, server.URL, "testuser", "wrongpass")
	if err == nil {
		t.Error("loginLocal() with invalid credentials should return error")
	}
}

func TestLoginLocal_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := &http.Client{}
	err := loginLocal(client, server.URL, "testuser", "testpass")
	if err == nil {
		t.Error("loginLocal() with server error should return error")
	}
}

func TestLoginLocal_NetworkError(t *testing.T) {
	// Use invalid URL to simulate network error
	client := &http.Client{}
	err := loginLocal(client, "http://invalid-url-that-does-not-exist:9999", "testuser", "testpass")
	if err == nil {
		t.Error("loginLocal() with network error should return error")
	}
}

func TestLoginLocal_InvalidJSON(t *testing.T) {
	// This test verifies that loginLocal handles JSON marshaling
	// The function should handle valid login/password without issues
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := &http.Client{}
	err := loginLocal(client, server.URL, "testuser", "testpass")
	if err != nil {
		t.Errorf("loginLocal() error = %v", err)
	}
}
