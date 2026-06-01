// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	fileConfigVersion     = 3
	fileConfigName        = "fortunnels.yml"
	envConfigPath         = "FORTUNNELS_CONFIG"
	errMsgConfigVersion   = "config version must be 3"
	errMsgConfigAuthtoken = "agent.authtoken is required"
)

// FileConfig is the on-disk fortunnels.yml schema (version 3).
type FileConfig struct {
	Version int `yaml:"version"`
	Agent   struct {
		Authtoken string `yaml:"authtoken"`
	} `yaml:"agent"`
}

// DefaultConfigPath returns the platform default config file path.
// FORTUNNELS_CONFIG overrides when set.
func DefaultConfigPath() (string, error) {
	if v := strings.TrimSpace(os.Getenv(envConfigPath)); v != "" {
		return v, nil
	}
	base, err := userConfigBaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "fortunnels", fileConfigName), nil
}

func userConfigBaseDir() (string, error) {
	if runtime.GOOS == "windows" {
		local := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
		if local == "" {
			return "", errors.New("LOCALAPPDATA is not set")
		}
		return local, nil
	}
	return os.UserConfigDir()
}

// LoadFileConfig reads and parses fortunnels.yml at path.
func LoadFileConfig(path string) (*FileConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var fc FileConfig
	if err := yaml.Unmarshal(data, &fc); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &fc, nil
}

// ValidateFileConfig checks schema requirements for ngrok-style config check.
func ValidateFileConfig(c *FileConfig) error {
	if c == nil {
		return errors.New("config is nil")
	}
	if c.Version != fileConfigVersion {
		return fmt.Errorf("%s (got %d)", errMsgConfigVersion, c.Version)
	}
	if strings.TrimSpace(c.Agent.Authtoken) == "" {
		return errors.New(errMsgConfigAuthtoken)
	}
	return nil
}

// SaveAuthtoken writes or merges authtoken into fortunnels.yml with secure permissions.
func SaveAuthtoken(path, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("authtoken is empty")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	fc := &FileConfig{Version: fileConfigVersion}
	fc.Agent.Authtoken = token
	if _, err := os.Stat(path); err == nil {
		existing, err := LoadFileConfig(path)
		if err == nil && existing != nil {
			fc.Version = existing.Version
			if fc.Version == 0 {
				fc.Version = fileConfigVersion
			}
		}
	}
	out, err := yaml.Marshal(fc)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}

// AuthtokenFromFile loads agent.authtoken from the default config path when present.
func AuthtokenFromFile() string {
	path, err := DefaultConfigPath()
	if err != nil {
		return ""
	}
	fc, err := LoadFileConfig(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(fc.Agent.Authtoken)
}
