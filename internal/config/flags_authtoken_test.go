// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package config

import (
	"os"
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

func TestApplyConfigFileAuthtokenSkippedWhenLoginSet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fortunnels.yml")
	require.NoError(t, SaveAuthtoken(path, "ft_from_config_file"))
	t.Setenv("FORTUNNELS_CONFIG", path)

	cfg := &Config{Login: "alice", Password: "secret"}
	applyConfigFileAuthtoken(cfg)
	require.Empty(t, cfg.Token)
	require.False(t, cfg.TokenFromConfigFile)
}

func TestApplyConfigFileAuthtokenSkippedWhenLoginSetWarns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fortunnels.yml")
	require.NoError(t, SaveAuthtoken(path, "ft_from_config_file"))
	t.Setenv("FORTUNNELS_CONFIG", path)

	stderrPath := filepath.Join(dir, "stderr.log")
	stderrFile, err := os.Create(stderrPath)
	require.NoError(t, err)
	origStderr := os.Stderr
	os.Stderr = stderrFile

	cfg := &Config{Login: "alice", Password: "secret"}
	applyConfigFileAuthtoken(cfg)
	_ = stderrFile.Close()
	os.Stderr = origStderr

	b, err := os.ReadFile(stderrPath)
	require.NoError(t, err)
	require.Contains(t, string(b), "authtoken ignored")
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

func TestApplyConfigFileAuthtokenSetsFromConfigFileFlag(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fortunnels.yml")
	require.NoError(t, SaveAuthtoken(path, "ft_from_config_file"))
	t.Setenv("FORTUNNELS_CONFIG", path)

	cfg := &Config{}
	applyConfigFileAuthtoken(cfg)
	require.Equal(t, "ft_from_config_file", cfg.Token)
	require.True(t, cfg.TokenFromConfigFile)
}
