// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package config

import (
	"fmt"
	"strings"
)

// IsUDPReverseMode reports whether advanced reverse UDP proxy flags are both set.
func IsUDPReverseMode(cfg *Config) bool {
	if cfg == nil {
		return false
	}
	return strings.TrimSpace(cfg.UDPListen) != "" && strings.TrimSpace(cfg.UDPDst) != ""
}

// IsUDPExposeLocalMode reports ngrok-style UDP when protocol is udp and reverse flags are absent.
func IsUDPExposeLocalMode(cfg *Config) bool {
	if cfg == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(cfg.Protocol), protoUDP) && !IsUDPReverseMode(cfg)
}

// ValidateUDPModeFlags ensures reverse-mode flags are provided together.
func ValidateUDPModeFlags(cfg *Config) error {
	if cfg == nil || !strings.EqualFold(strings.TrimSpace(cfg.Protocol), protoUDP) {
		return nil
	}
	hasListen := strings.TrimSpace(cfg.UDPListen) != ""
	hasDst := strings.TrimSpace(cfg.UDPDst) != ""
	if hasListen != hasDst {
		return fmt.Errorf("UDP reverse mode requires both --udp-listen and --udp-dst; omit both for expose-local mode (e.g. fortunnels udp 9000)")
	}
	return nil
}

// ValidateUDPExposeLocalOptions rejects unsupported expose-local combinations.
func ValidateUDPExposeLocalOptions(cfg *Config) error {
	if !IsUDPExposeLocalMode(cfg) {
		return nil
	}
	if plane := strings.ToLower(strings.TrimSpace(cfg.DataPlane)); plane != "" && plane != "ws" {
		return fmt.Errorf("UDP expose-local mode requires WS data-plane; use --udp-listen and --udp-dst for advanced reverse mode with quic/dtls")
	}
	if cfg.Encrypt {
		return fmt.Errorf("UDP expose-local mode does not support --encrypt yet; use advanced reverse mode or disable encryption")
	}
	return nil
}
