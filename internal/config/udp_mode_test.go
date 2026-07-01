// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateUDPModeFlags(t *testing.T) {
	tests := []struct {
		name      string
		cfg       *Config
		wantErr   bool
		errSubstr string
	}{
		{
			name:    "expose-local no flags",
			cfg:     &Config{Protocol: protoUDP},
			wantErr: false,
		},
		{
			name: "reverse both flags",
			cfg: &Config{
				Protocol:  protoUDP,
				UDPListen: ":5353",
				UDPDst:    "127.0.0.1:53",
			},
			wantErr: false,
		},
		{
			name: "only listen",
			cfg: &Config{
				Protocol:  protoUDP,
				UDPListen: ":5353",
			},
			wantErr:   true,
			errSubstr: "both --udp-listen and --udp-dst",
		},
		{
			name: "only dst",
			cfg: &Config{
				Protocol: protoUDP,
				UDPDst:   "127.0.0.1:53",
			},
			wantErr:   true,
			errSubstr: "both --udp-listen and --udp-dst",
		},
		{
			name: "uppercase UDP protocol",
			cfg: &Config{
				Protocol:   "UDP",
				TargetAddr: "127.0.0.1:9000",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateUDPModeFlags(tt.cfg)
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errSubstr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestIsUDPExposeLocalMode(t *testing.T) {
	require.True(t, IsUDPExposeLocalMode(&Config{Protocol: protoUDP}))
	require.False(t, IsUDPExposeLocalMode(&Config{
		Protocol:  protoUDP,
		UDPListen: ":1",
		UDPDst:    "127.0.0.1:1",
	}))
}

func TestValidateUDPExposeLocalOptions(t *testing.T) {
	err := ValidateUDPExposeLocalOptions(&Config{Protocol: protoUDP, DataPlane: "quic"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "WS data-plane")

	err = ValidateUDPExposeLocalOptions(&Config{Protocol: protoUDP, Encrypt: true, PSK: "12345678901234567890123456789012"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "--encrypt")
}
