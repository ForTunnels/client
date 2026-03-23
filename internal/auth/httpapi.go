// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/fortunnels/client/internal/config"
)

// csrfCookieName matches internal/security/csrf.go (double-submit cookie).
const csrfCookieName = "csrf_token"

// SetupAuthentication configures HTTP client and authentication for tunnel creation.
// Returns (httpClient, bearerToken, csrfToken, error). csrfToken is set after login/password
// when the server issues a csrf_token cookie (needed for session POST/DELETE when CSRF is enabled).
func SetupAuthentication(cfg *config.Config) (*http.Client, string, string, error) {
	if strings.TrimSpace(cfg.Token) != "" {
		bearer := strings.TrimSpace(cfg.Token)
		return nil, bearer, "", nil
	}
	if strings.TrimSpace(cfg.Login) != "" && strings.TrimSpace(cfg.Password) != "" {
		jar, err := cookiejar.New(nil)
		if err != nil {
			return nil, "", "", fmt.Errorf("create cookie jar: %w", err)
		}
		httpClient := &http.Client{Timeout: 10 * time.Second, Jar: jar}
		if err := loginLocal(httpClient, cfg.ServerURL, cfg.Login, cfg.Password); err != nil {
			return nil, "", "", fmt.Errorf("login failed: %w", err)
		}
		if err := bootstrapCSRFCookie(httpClient, cfg.ServerURL); err != nil {
			return nil, "", "", err
		}
		csrf := csrfTokenFromJar(httpClient, cfg.ServerURL)
		return httpClient, "", csrf, nil
	}
	return nil, "", "", nil
}

// loginLocal performs POST /auth/login-local and stores cookie in provided http.Client jar
func loginLocal(client *http.Client, serverURL, login, password string) error {
	payload := map[string]string{"login": login, "password": password}
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal login payload: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", serverURL+"/auth/login-local", bytes.NewBuffer(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("login failed: HTTP %d", resp.StatusCode)
	}
	return nil
}

// bootstrapCSRFCookie performs a safe GET so the server can set csrf_token (CSRF middleware on GET).
func bootstrapCSRFCookie(client *http.Client, serverURL string) error {
	u := strings.TrimRight(serverURL, "/") + "/auth/me"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("csrf bootstrap: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("csrf bootstrap: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= http.StatusInternalServerError {
		return fmt.Errorf("csrf bootstrap: server returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func csrfTokenFromJar(client *http.Client, serverURL string) string {
	if client == nil || client.Jar == nil {
		return ""
	}
	u, err := url.Parse(serverURL)
	if err != nil {
		return ""
	}
	if u.Path == "" {
		u.Path = "/"
	}
	for _, c := range client.Jar.Cookies(u) {
		if c.Name == csrfCookieName && c.Value != "" {
			return c.Value
		}
	}
	return ""
}
