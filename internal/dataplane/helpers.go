// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/fortunnels/client/internal/config"
)

const (
	wsReadTimeout       = 2*config.MaxPingInterval + 15*time.Second
	reconnectRetryDelay = 200 * time.Millisecond
	udpMaxPacketSize    = 65535
	udpDatagramMaxSize  = 65507
	headerDataPlaneAuth = "X-Fortunnels-DP-Auth"
	schemeHTTP          = "http"
	schemeHTTPS         = "https"
)

func encodePreface(fields map[string]string) ([]byte, error) {
	b, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("marshal preface: %w", err)
	}
	return append(b, '\n'), nil
}

func buildWebSocketURL(serverURL, tunnelID, authToken string) (wsURL, origin, token string, err error) {
	u, parseErr := url.Parse(serverURL)
	if parseErr != nil || u.Scheme == "" || u.Host == "" {
		if parseErr == nil {
			parseErr = fmt.Errorf("invalid server url")
		}
		return "", "", "", parseErr
	}

	var originScheme, wsScheme string
	switch u.Scheme {
	case schemeHTTP:
		originScheme = schemeHTTP
		wsScheme = "ws"
	case schemeHTTPS:
		originScheme = schemeHTTPS
		wsScheme = "wss"
	default:
		return "", "", "", fmt.Errorf("unsupported server scheme: %s", u.Scheme)
	}

	u.Scheme = wsScheme
	u.Path = "/ws"
	q := u.Query()
	q.Set("mode", "data")
	q.Set("tunnel_id", tunnelID)
	q.Del("auth")
	u.RawQuery = q.Encode()

	wsURL = u.String()
	origin = originScheme + "://" + u.Host
	return wsURL, origin, strings.TrimSpace(authToken), nil
}
