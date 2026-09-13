// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package auth

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/fortunnels/client/internal/config"
)

// MapCreateTunnelAuthError returns a friendly error when tunnel create failed because
// the server rejected a non-empty bearer credential (401).
func MapCreateTunnelAuthError(createErr error, bearer string, fromConfigFile bool, protocol ...string) error {
	if createErr == nil || strings.TrimSpace(bearer) == "" {
		return nil
	}
	if !isUnauthorizedTunnelCreateError(createErr) {
		return nil
	}
	return errBearerRejectedAsGuest(fromConfigFile, firstProtocol(protocol))
}

func isUnauthorizedTunnelCreateError(err error) bool {
	type codedHTTPError interface {
		HTTPStatus() int
		ErrorCode() string
	}
	var coded codedHTTPError
	if errors.As(err, &coded) {
		return coded.HTTPStatus() == http.StatusUnauthorized || coded.ErrorCode() == "authentication_required"
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "status 401") || strings.Contains(msg, "status code 401")
}

// TunnelGuestSignals carries create-response fields used to detect guest fallback.
type TunnelGuestSignals struct {
	IsGuest bool
	UserID  int64
}

// TunnelLooksLikeGuest reports whether a tunnel create response indicates guest ownership.
// POST /api/tunnels may omit is_guest even for guest tunnels; user_id 0 is the reliable signal.
func TunnelLooksLikeGuest(s TunnelGuestSignals) bool {
	if s.IsGuest {
		return true
	}
	return s.UserID == 0
}

// CheckBearerNotRejectedAsGuest returns an error when the server created a guest tunnel
// despite a non-empty bearer token (revoked, invalid, or expired on server).
func CheckBearerNotRejectedAsGuest(bearer string, fromConfigFile bool, tun TunnelGuestSignals, protocol ...string) error {
	if strings.TrimSpace(bearer) == "" || !TunnelLooksLikeGuest(tun) {
		return nil
	}
	return errBearerRejectedAsGuest(fromConfigFile, firstProtocol(protocol))
}

func errBearerRejectedAsGuest(fromConfigFile bool, protocol string) error {
	if requiresAccount(protocol) {
		if fromConfigFile {
			return fmt.Errorf(
				"saved credentials in %s were not accepted by the server (revoked, invalid, or expired); sign in with -login and -pass-stdin, then run this command again",
				configPathHint(),
			)
		}
		return fmt.Errorf("credentials were not accepted by the server (revoked, invalid, or expired); sign in with -login and -pass-stdin, then run this command again")
	}
	if fromConfigFile {
		configPath := configPathHint()
		return fmt.Errorf(
			"saved authtoken in %s was not accepted by the server (revoked, invalid, or expired); "+
				"update with `fortunnels config add-authtoken <new-token>` (create a token in dashboard Settings first), "+
				"or remove agent.authtoken from %s to run without credentials (guest mode), "+
				"or use --login with password or --token with a valid token",
			configPath, configPath,
		)
	}
	return fmt.Errorf(
		"bearer token was not accepted by the server (revoked, invalid, or expired); " +
			"use `fortunnels config add-authtoken <new-token>` to save a new token (create a token in dashboard Settings first), " +
			"or clear explicit --token / FORTUNNELS_TOKEN to run without credentials (guest mode), " +
			"or use --login with password or --token with a valid token",
	)
}

func requiresAccount(protocol string) bool {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "tcp", "udp":
		return true
	default:
		return false
	}
}

func firstProtocol(protocol []string) string {
	if len(protocol) == 0 {
		return ""
	}
	return protocol[0]
}

func configPathHint() string {
	path, err := config.DefaultConfigPath()
	if err != nil {
		return "fortunnels.yml"
	}
	return path
}
