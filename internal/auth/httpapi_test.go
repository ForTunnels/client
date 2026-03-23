// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

//go:build integration

package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fortunnels/client/internal/config"
)

func TestSetupAuthentication_WithToken(t *testing.T) {
	cfg := &config.Config{
		Token:     "bearer-token-123",
		ServerURL: "https://example.com",
	}

	client, bearer, csrf, err := SetupAuthentication(cfg)
	require.NoError(t, err, "SetupAuthentication()")
	assert.Equal(t, "bearer-token-123", bearer, "SetupAuthentication() bearer")
	assert.Empty(t, csrf, "SetupAuthentication() csrf with token")
	assert.Nil(t, client, "SetupAuthentication() with token should not create HTTP client")
}

func TestSetupAuthentication_WithLoginPassword(t *testing.T) {
	const testCSRF = "bootstrap-csrf-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/auth/login-local" && r.Method == http.MethodPost:
			var payload map[string]string
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if payload["login"] == "testuser" && payload["password"] == "testpass" {
				http.SetCookie(w, &http.Cookie{Name: "session", Value: "session-token", Path: "/"})
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
		case r.URL.Path == "/auth/me" && r.Method == http.MethodGet:
			// Simulates real server: CSRF cookie after authenticated GET
			http.SetCookie(w, &http.Cookie{Name: csrfCookieName, Value: testCSRF, Path: "/"})
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cfg := &config.Config{
		Login:     "testuser",
		Password:  "testpass",
		ServerURL: server.URL,
	}

	client, bearer, csrf, err := SetupAuthentication(cfg)
	require.NoError(t, err, "SetupAuthentication()")
	assert.Empty(t, bearer, "SetupAuthentication() bearer")
	assert.Equal(t, testCSRF, csrf, "SetupAuthentication() should return CSRF from bootstrap GET")
	require.NotNil(t, client, "SetupAuthentication() with login/password should create HTTP client")
	assert.NotNil(t, client.Jar, "SetupAuthentication() should set cookie jar")

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	if serverURL.Path == "" {
		serverURL.Path = "/"
	}
	assert.GreaterOrEqual(t, len(client.Jar.Cookies(serverURL)), 1, "jar should hold session and/or csrf cookies")
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

	_, _, _, err := SetupAuthentication(cfg)
	require.Error(t, err, "SetupAuthentication() with invalid credentials should return error")
}

func TestSetupAuthentication_Empty(t *testing.T) {
	cfg := &config.Config{
		ServerURL: "https://example.com",
	}

	client, bearer, csrf, err := SetupAuthentication(cfg)
	require.NoError(t, err, "SetupAuthentication()")
	assert.Empty(t, bearer, "SetupAuthentication() bearer")
	assert.Empty(t, csrf, "SetupAuthentication() csrf")
	assert.Nil(t, client, "SetupAuthentication() with empty config should not create HTTP client")
}

func TestSetupAuthentication_WithToken_Whitespace(t *testing.T) {
	cfg := &config.Config{
		Token:     "  bearer-token-123  ",
		ServerURL: "https://example.com",
	}

	_, bearer, csrf, err := SetupAuthentication(cfg)
	require.NoError(t, err, "SetupAuthentication()")
	assert.Equal(t, "bearer-token-123", bearer, "SetupAuthentication() bearer")
	assert.Empty(t, csrf, "SetupAuthentication() csrf with token")
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
	assert.NoError(t, err, "loginLocal()")
}

func TestLoginLocal_InvalidCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	client := &http.Client{}
	err := loginLocal(client, server.URL, "testuser", "wrongpass")
	require.Error(t, err, "loginLocal() with invalid credentials should return error")
}

func TestLoginLocal_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := &http.Client{}
	err := loginLocal(client, server.URL, "testuser", "testpass")
	require.Error(t, err, "loginLocal() with server error should return error")
}

func TestLoginLocal_NetworkError(t *testing.T) {
	// Use invalid URL to simulate network error
	client := &http.Client{}
	err := loginLocal(client, "http://invalid-url-that-does-not-exist:9999", "testuser", "testpass")
	require.Error(t, err, "loginLocal() with network error should return error")
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
	assert.NoError(t, err, "loginLocal()")
}
