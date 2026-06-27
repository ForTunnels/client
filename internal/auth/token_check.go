// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package auth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// validateConfigAuthtoken returns an error when fortunnels.yml authtoken looks expired or malformed.
func validateConfigAuthtoken(token string, fromConfigFile bool) error {
	if !fromConfigFile || strings.TrimSpace(token) == "" {
		return nil
	}
	if isBearerTokenExpiredOrMalformed(token) {
		return fmt.Errorf(
			"fortunnels.yml authtoken is expired or invalid; update with fortunnels config add-authtoken <token> or remove agent.authtoken to use guest mode",
		)
	}
	return nil
}

func isBearerTokenExpiredOrMalformed(token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	if strings.HasPrefix(token, "ft_") {
		return false
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return true
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return true
	}
	var claims struct {
		Exp float64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return true
	}
	if claims.Exp == 0 {
		return false
	}
	return time.Now().Unix() >= int64(claims.Exp)
}
