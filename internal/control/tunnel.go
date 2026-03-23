// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	protocolv1 "github.com/fortunnels/client/shared/protocol/v1"
)

type Response = protocolv1.Tunnel

// createTunnelWithClient allows passing http.Client (with cookiejar), bearer token, and optional CSRF header for session auth.
func CreateTunnelWithClient(
	serverURL, localAddr, protocol, userID string,
	client *http.Client,
	bearer, csrf string,
) (*Response, error) {
	requestBody := protocolv1.TunnelCreateRequest{
		TargetAddr: localAddr,
		Protocol:   protocol,
		UserID:     userID,
	}
	if strings.EqualFold(protocol, "https") {
		// Автоконфигурация для localhost: разрешаем self-signed и подставляем SNI
		if h, _, err := net.SplitHostPort(localAddr); err == nil {
			if h == "localhost" || h == "127.0.0.1" { // локальная разработка
				insecure := true
				requestBody.TLSInsecureSkipVerify = &insecure
				requestBody.TLSServerName = "localhost"
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
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(bearer))
	}
	if strings.TrimSpace(csrf) != "" {
		req.Header.Set("X-CSRF-Token", strings.TrimSpace(csrf))
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

// rewriteIngressPublicURL replaces loopback hosts in tcp:// and udp:// URLs with the API server's
// hostname so the CLI shows an address remote users can reach (backward-compatible with older servers).
func rewriteIngressPublicURL(serverURL, publicURL string) string {
	if strings.TrimSpace(serverURL) == "" {
		return publicURL
	}
	u, err := url.Parse(publicURL)
	if err != nil || u.Host == "" {
		return publicURL
	}
	if u.Scheme != "tcp" && u.Scheme != "udp" {
		return publicURL
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		return publicURL
	}
	switch strings.ToLower(host) {
	case "127.0.0.1", "0.0.0.0", "::1", "[::1]", "::", "[::]":
	default:
		return publicURL
	}
	base, err := url.Parse(serverURL)
	if err != nil {
		return publicURL
	}
	displayHost := base.Hostname()
	if displayHost == "" {
		return publicURL
	}
	if strings.Contains(displayHost, ":") {
		return fmt.Sprintf("%s://[%s]:%s", u.Scheme, displayHost, port)
	}
	return fmt.Sprintf("%s://%s:%s", u.Scheme, displayHost, port)
}

// DeleteTunnelWithClient sends DELETE /api/tunnels?id=<id> using the given client.
// Best-effort cleanup to avoid orphan tunnels when startup fails after creation.
func DeleteTunnelWithClient(serverURL, tunnelID string, client *http.Client, bearer, csrf string) {
	if tunnelID == "" {
		return
	}
	hc := client
	if hc == nil {
		hc = &http.Client{Timeout: 5 * time.Second}
	}
	log.Printf("[WARN] attempting cleanup of tunnel %s", tunnelID)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	params := url.Values{}
	params.Set("id", tunnelID)
	req, err := http.NewRequestWithContext(ctx, "DELETE", serverURL+"/api/tunnels?"+params.Encode(), http.NoBody)
	if err != nil {
		log.Printf("[ERROR] cleanup failed tunnelID=%s: %v", tunnelID, err)
		return
	}
	if strings.TrimSpace(bearer) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(bearer))
	}
	if strings.TrimSpace(csrf) != "" {
		req.Header.Set("X-CSRF-Token", strings.TrimSpace(csrf))
	}
	resp, err := hc.Do(req)
	if err != nil {
		log.Printf("[ERROR] cleanup failed tunnelID=%s: %v", tunnelID, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		log.Printf("[INFO] cleanup succeeded tunnelID=%s", tunnelID)
	} else {
		log.Printf("[ERROR] cleanup failed tunnelID=%s status=%d", tunnelID, resp.StatusCode)
	}
}

// printTunnelInfo displays comprehensive information about the created tunnel.
// serverURL is the API base URL (e.g. https://fortunnels.ru); used to fix tcp/udp loopback public URLs in the CLI.
func PrintTunnelInfo(serverURL string, tunnel *Response) {
	PrintTunnelInfoWithOutput(StdOutput{}, serverURL, tunnel)
}

func PrintTunnelInfoWithOutput(out Output, serverURL string, tunnel *Response) {
	if out == nil {
		out = StdOutput{}
	}
	out.Printf("✅ Tunnel created successfully!\n")
	out.Printf("🔗 Public URL: %s\n", rewriteIngressPublicURL(serverURL, tunnel.PublicURL))
	out.Printf("🆔 Tunnel ID: %s\n", tunnel.ID)
	out.Printf("📊 Status: %s\n", tunnel.Status)
	if tunnel.IsGuest {
		out.Printf(
			"ℹ️ Гостевой туннель: срок жизни до %s, лимит трафика 1 GB.\n",
			tunnel.ExpiresAt.Local().Format("2006-01-02 15:04:05"),
		)
	}
}

// printHTTPHints prints example curl commands for path-based and host-based usage.
func PrintHTTPHints(serverURL string, t *Response) {
	PrintHTTPHintsWithOutput(StdOutput{}, serverURL, t)
}

func PrintHTTPHintsWithOutput(out Output, serverURL string, t *Response) {
	if out == nil {
		out = StdOutput{}
	}
	out.Println("\n💡 Usage hints (HTTP):")
	out.Printf(
		"- Path-based (dev): %s/t/%s\n",
		serverURL,
		t.ID,
	)
	out.Printf("- Host-based: %s (use Host header)\n", t.PublicURL)
	_ = os.Stdout.Sync()
}
