// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package config

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestApplyConfigFileAuthtoken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fortunnels.yml")
	require.NoError(t, SaveAuthtoken(path, "ft_from_config_file"))
	t.Setenv("FORTUNNELS_CONFIG", path)

	cfg := &Config{}
	applyConfigFileAuthtoken(cfg)
	require.Equal(t, "ft_from_config_file", cfg.Token)
}

func TestApplyConfigFileAuthtokenSkippedWhenTokenSet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fortunnels.yml")
	require.NoError(t, SaveAuthtoken(path, "ft_from_config_file"))
	t.Setenv("FORTUNNELS_CONFIG", path)

	cfg := &Config{Token: "ft_from_flag"}
	applyConfigFileAuthtoken(cfg)
	require.Equal(t, "ft_from_flag", cfg.Token)
}
