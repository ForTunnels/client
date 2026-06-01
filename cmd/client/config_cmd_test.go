// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunConfigCheckMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fortunnels.yml")
	t.Setenv("FORTUNNELS_CONFIG", path)
	code := runConfigCheck()
	require.Equal(t, 1, code)
}

func TestRunConfigAddAuthtokenAndCheck(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fortunnels.yml")
	t.Setenv("FORTUNNELS_CONFIG", path)

	code := runConfigAddAuthtoken([]string{"ft_cli_test_token_value"})
	require.Equal(t, 0, code)
	_, err := os.Stat(path)
	require.NoError(t, err)

	code = runConfigCheck()
	require.Equal(t, 0, code)
}

func TestRunConfigAddAuthtokenUsage(t *testing.T) {
	require.Equal(t, 2, runConfigAddAuthtoken(nil))
}
