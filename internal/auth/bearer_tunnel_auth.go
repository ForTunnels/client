// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package auth

import (
	"fmt"
	"strings"

	"github.com/fortunnels/client/internal/config"
)

// MapCreateTunnelAuthError returns a friendly error when tunnel create failed because
// the server rejected a non-empty bearer credential (401).
func MapCreateTunnelAuthError(createErr error, bearer string, fromConfigFile bool) error {
	if createErr == nil || strings.TrimSpace(bearer) == "" {
		return nil
	}
	if !isUnauthorizedTunnelCreateError(createErr) {
		return nil
	}
	return errBearerRejectedAsGuest(fromConfigFile)
}

func isUnauthorizedTunnelCreateError(err error) bool {
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
func CheckBearerNotRejectedAsGuest(bearer string, fromConfigFile bool, tun TunnelGuestSignals) error {
	if strings.TrimSpace(bearer) == "" || !TunnelLooksLikeGuest(tun) {
		return nil
	}
	return errBearerRejectedAsGuest(fromConfigFile)
}

func errBearerRejectedAsGuest(fromConfigFile bool) error {
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

func configPathHint() string {
	path, err := config.DefaultConfigPath()
	if err != nil {
		return "fortunnels.yml"
	}
	return path
}
