// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fortunnels/client/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetupAuthentication_ExplicitTokenFlagWinsOverLogin(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected request to %s", r.URL.Path)
	}))
	t.Cleanup(srv.Close)

	cfg := &config.Config{
		ServerURL:         srv.URL,
		Token:             "explicit-token",
		TokenFlagProvided: true,
		Login:             "alice",
		Password:          "secret",
	}
	client, bearer, csrf, err := SetupAuthentication(cfg)
	require.NoError(t, err)
	assert.Nil(t, client)
	assert.Equal(t, "explicit-token", bearer)
	assert.Empty(t, csrf)
}

func TestSetupAuthentication_LoginWinsOverConfigFileToken(t *testing.T) {
	t.Parallel()

	var sawLogin bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/login-local":
			sawLogin = true
			w.WriteHeader(http.StatusOK)
		case "/auth/me":
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	cfg := &config.Config{
		ServerURL:           srv.URL,
		Token:               "ft_from_config_file",
		TokenFromConfigFile: true,
		Login:               "alice",
		Password:            "secret",
	}
	client, bearer, _, err := SetupAuthentication(cfg)
	require.NoError(t, err)
	assert.True(t, sawLogin)
	assert.NotNil(t, client)
	assert.Empty(t, bearer)
}

func TestSetupAuthentication_ConfigFileTokenBearer(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected request to %s", r.URL.Path)
	}))
	t.Cleanup(srv.Close)

	cfg := &config.Config{
		ServerURL:           srv.URL,
		Token:               "ft_from_config_file",
		TokenFromConfigFile: true,
	}
	client, bearer, csrf, err := SetupAuthentication(cfg)
	require.NoError(t, err)
	assert.Nil(t, client)
	assert.Equal(t, "ft_from_config_file", bearer)
	assert.Empty(t, csrf)
}

func TestSetupAuthentication_ConfigFileMalformedJWTFails(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected request to %s", r.URL.Path)
	}))
	t.Cleanup(srv.Close)

	cfg := &config.Config{
		ServerURL:           srv.URL,
		Token:               "stale-invalid-token",
		TokenFromConfigFile: true,
	}
	client, bearer, csrf, err := SetupAuthentication(cfg)
	require.Error(t, err)
	assert.Nil(t, client)
	assert.Empty(t, bearer)
	assert.Empty(t, csrf)
	assert.Contains(t, err.Error(), "fortunnels.yml authtoken is expired or invalid")
}
