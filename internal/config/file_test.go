// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestSaveLoadValidateFileConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fortunnels.yml")
	token := "ft_testtoken1234567890"
	require.NoError(t, SaveAuthtoken(path, token))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	fc, err := LoadFileConfig(path)
	require.NoError(t, err)
	require.NoError(t, ValidateFileConfig(fc))
	require.Equal(t, token, fc.Agent.Authtoken)
	require.Equal(t, fileConfigVersion, fc.Version)
}

func TestValidateFileConfigErrors(t *testing.T) {
	require.Error(t, ValidateFileConfig(&FileConfig{Version: 2}))
	require.Error(t, ValidateFileConfig(&FileConfig{Version: 3}))
}

func TestDefaultConfigPathOverride(t *testing.T) {
	t.Setenv(envConfigPath, "/tmp/custom-fortunnels.yml")
	path, err := DefaultConfigPath()
	require.NoError(t, err)
	require.Equal(t, "/tmp/custom-fortunnels.yml", path)
}

func TestDefaultConfigPathWindowsUsesLocalAppData(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows only")
	}
	t.Setenv(envConfigPath, "")
	t.Setenv("LOCALAPPDATA", `C:\Users\test\AppData\Local`)
	path, err := DefaultConfigPath()
	require.NoError(t, err)
	require.Contains(t, path, `AppData\Local\fortunnels\fortunnels.yml`)
}

func TestAuthtokenFromFileMissing(t *testing.T) {
	t.Setenv(envConfigPath, filepath.Join(t.TempDir(), "missing.yml"))
	require.Empty(t, AuthtokenFromFile())
}

func TestSaveAuthtokenMergeKeepsVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fortunnels.yml")
	initial := FileConfig{Version: 3}
	initial.Agent.Authtoken = "old"
	data, err := yaml.Marshal(initial)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600))
	require.NoError(t, SaveAuthtoken(path, "ft_newtoken"))
	fc, err := LoadFileConfig(path)
	require.NoError(t, err)
	require.Equal(t, "ft_newtoken", fc.Agent.Authtoken)
	require.Equal(t, 3, fc.Version)
}
