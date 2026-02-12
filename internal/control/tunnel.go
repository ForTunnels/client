// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// Response is the JSON representation returned by the server when
// creating a tunnel via REST API.
type Response struct {
	ID          string    `json:"id"`
	UserID      int64     `json:"user_id"`
	Protocol    string    `json:"protocol"`
	TargetAddr  string    `json:"target_addr"`
	PublicURL   string    `json:"public_url"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	LastActive  time.Time `json:"last_active"`
	Connections int       `json:"connections"`
	IsGuest     bool      `json:"is_guest"`
	BytesUsed   int64     `json:"bytes_used"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// createTunnelWithClient allows passing http.Client (with cookiejar) and bearer token
func CreateTunnelWithClient(
	serverURL, localAddr, protocol, userID string,
	client *http.Client,
	bearer string,
) (*Response, error) {
	requestBody := map[string]interface{}{
		"target_addr": localAddr,
		"protocol":    protocol,
		"user_id":     userID,
	}
	if strings.EqualFold(protocol, "https") {
		// Автоконфигурация для localhost: разрешаем self-signed и подставляем SNI
		if h, _, err := net.SplitHostPort(localAddr); err == nil {
			if h == "localhost" || h == "127.0.0.1" { // локальная разработка
				requestBody["tls_insecure_skip_verify"] = true
				requestBody["tls_server_name"] = "localhost"
			}
		}
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return nil, err
	}

	// Build request
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", serverURL+"/api/tunnels", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(bearer) != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	// Select client
	var hc *http.Client
	if client != nil {
		hc = client
	} else {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		// Try to read error message from response body
		//nolint:errcheck // best-effort read of error body
		bodyBytes, _ := io.ReadAll(resp.Body)
		bodyStr := strings.TrimSpace(string(bodyBytes))
		if bodyStr != "" {
			return nil, fmt.Errorf("server returned status %d: %s", resp.StatusCode, bodyStr)
		}
		return nil, fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	var tunnel Response
	if err := json.NewDecoder(resp.Body).Decode(&tunnel); err != nil {
		return nil, err
	}

	return &tunnel, nil
}

// printTunnelInfo displays comprehensive information about the created tunnel.
func PrintTunnelInfo(tunnel *Response) {
	fmt.Printf("✅ Tunnel created successfully!\n")
	fmt.Printf("🔗 Public URL: %s\n", tunnel.PublicURL)
	fmt.Printf("🆔 Tunnel ID: %s\n", tunnel.ID)
	fmt.Printf("📊 Status: %s\n", tunnel.Status)
	if tunnel.IsGuest {
		fmt.Printf(
			"ℹ️ Гостевой туннель: срок жизни до %s, лимит трафика 1 GB.\n",
			tunnel.ExpiresAt.Local().Format("2006-01-02 15:04:05"),
		)
	}
}

// printHTTPHints prints example curl commands for path-based and host-based usage.
func PrintHTTPHints(serverURL string, t *Response) {
	fmt.Println("\n💡 Usage hints (HTTP):")
	fmt.Printf(
		"- Path-based (dev): %s/t/%s\n",
		serverURL,
		t.ID,
	)
	fmt.Printf("- Host-based: %s (use Host header)\n", t.PublicURL)
	fmt.Println("- Default: stays running")
	_ = os.Stdout.Sync()
}
