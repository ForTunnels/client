// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package control

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/websocket"
)

// buildControlWebSocketURL builds a control-plane WebSocket URL for tunnel watch mode.
func buildControlWebSocketURL(serverURL, tunnelID string) (string, error) {
	u, err := url.Parse(serverURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		if err == nil {
			err = fmt.Errorf("invalid server url")
		}
		return "", err
	}

	var wsScheme string
	switch u.Scheme {
	case "http":
		wsScheme = "ws"
	case "https":
		wsScheme = "wss"
	default:
		return "", fmt.Errorf("unsupported server scheme: %s", u.Scheme)
	}

	u.Scheme = wsScheme
	u.Path = "/ws"
	q := u.Query()
	q.Set("watch", tunnelID)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func wsDialHeaders(httpClient *http.Client, serverURL, bearer string) http.Header {
	h := http.Header{}
	if b := strings.TrimSpace(bearer); b != "" {
		h.Set("Authorization", "Bearer "+b)
	}
	if httpClient == nil || httpClient.Jar == nil {
		return h
	}
	u, err := url.Parse(serverURL)
	if err != nil {
		return h
	}
	for _, c := range httpClient.Jar.Cookies(u) {
		if c.Name == "" {
			continue
		}
		h.Add("Cookie", c.Name+"="+c.Value)
	}
	return h
}

func dialControlWebSocket(httpClient *http.Client, serverURL, tunnelID, bearer string) (*websocket.Conn, *http.Response, error) {
	wsURL, err := buildControlWebSocketURL(serverURL, tunnelID)
	if err != nil {
		return nil, nil, err
	}
	return websocket.DefaultDialer.Dial(wsURL, wsDialHeaders(httpClient, serverURL, bearer))
}
