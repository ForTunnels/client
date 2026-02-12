// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"

	"github.com/fortunnels/client/internal/config"
)

// SetupAuthentication configures HTTP client and authentication for tunnel creation.
// Returns (httpClient, bearerToken, error).
func SetupAuthentication(cfg *config.Config) (*http.Client, string, error) {
	var httpClient *http.Client
	var bearer string

	if strings.TrimSpace(cfg.Token) != "" {
		bearer = strings.TrimSpace(cfg.Token)
	} else if strings.TrimSpace(cfg.Login) != "" && strings.TrimSpace(cfg.Password) != "" {
		jar, err := cookiejar.New(nil)
		if err != nil {
			return nil, "", fmt.Errorf("create cookie jar: %w", err)
		}
		httpClient = &http.Client{Timeout: 10 * time.Second, Jar: jar}
		// login-local to obtain session cookie
		if err := loginLocal(httpClient, cfg.ServerURL, cfg.Login, cfg.Password); err != nil {
			return nil, "", fmt.Errorf("login failed: %w", err)
		}
	}

	return httpClient, bearer, nil
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
