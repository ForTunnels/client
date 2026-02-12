// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package support

import (
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"strings"
)

// isBenignCopyError returns true for normal connection close conditions
// to avoid noisy logs when bridging half-closed TCP streams.
func IsBenignCopyError(err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	msg := err.Error()
	if strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset by peer") ||
		strings.Contains(msg, "stream closed") ||
		strings.Contains(msg, "EOF") { // treat wrapped EOFs as benign
		return true
	}
	return false
}

// getDefaultServerURL returns the default server URL taking environment override into account.
// 1) env FORTUNNELS_SERVER_URL if set
// 2) compiled defaultServerURL (can be overridden via -ldflags)
func GetDefaultServerURL(defaultServerURL string) string {
	if v := os.Getenv("FORTUNNELS_SERVER_URL"); strings.TrimSpace(v) != "" {
		return v
	}
	return defaultServerURL
}

// GetEnvTrimmed returns a trimmed environment variable value or empty string.
func GetEnvTrimmed(name string) string {
	if v := os.Getenv(name); strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return ""
}

const maxSecretSize = 64 * 1024

// ReadSecretFile reads a secret from a file and trims whitespace.
func ReadSecretFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read secret file: %w", err)
	}
	secret := strings.TrimSpace(string(data))
	if secret == "" {
		return "", errors.New("secret file is empty")
	}
	if len(secret) > maxSecretSize {
		return "", errors.New("secret file is too large")
	}
	return secret, nil
}

// ReadSecretStdin reads a secret from stdin and trims whitespace.
func ReadSecretStdin(label string) (string, error) {
	data, err := io.ReadAll(io.LimitReader(os.Stdin, maxSecretSize+1))
	if err != nil {
		return "", fmt.Errorf("read %s from stdin: %w", label, err)
	}
	if len(data) > maxSecretSize {
		return "", fmt.Errorf("%s from stdin is too large", label)
	}
	secret := strings.TrimSpace(string(data))
	if secret == "" {
		return "", fmt.Errorf("%s from stdin is empty", label)
	}
	return secret, nil
}

// parsePort parses port from string, accepts forms like 8000 or :8000
func ParsePort(s string) string {
	if s == "" {
		return ""
	}
	// accept forms like 8000 or :8000
	s = strings.TrimPrefix(s, ":")
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return ""
		}
	}
	return s
}

// looksLikeHostPort checks if string looks like host:port
func LooksLikeHostPort(s string) bool {
	// require non-empty host and numeric port
	i := strings.LastIndex(s, ":")
	if i <= 0 {
		return false
	}
	host := s[:i]
	port := s[i+1:]
	if host == "" || port == "" {
		return false
	}
	return ParsePort(port) != ""
}

// toUint32Size converts int to uint32 with size validation
func ToUint32Size(n int) (uint32, error) {
	return toUintSize[uint32](n, int64(math.MaxUint32), "uint32")
}

// toUint16Size converts int to uint16 with size validation
func ToUint16Size(n int) (uint16, error) {
	return toUintSize[uint16](n, int64(math.MaxUint16), "uint16")
}

// toUintSize is a generic helper for uint size validation
func toUintSize[T ~uint16 | ~uint32](n int, limit int64, label string) (T, error) {
	if n < 0 {
		return 0, fmt.Errorf("value exceeds %s range: %d", label, n)
	}
	// Compare directly when limit fits in int to avoid unnecessary int64 conversion
	// For uint16: limit is math.MaxUint16 (65535), which always fits in int
	// For uint32: limit is math.MaxUint32 (4294967295), which fits in int on 64-bit systems
	// On 32-bit systems, if limit > math.MaxInt, we need int64 comparison
	if limit <= int64(math.MaxInt) {
		if n > int(limit) {
			return 0, fmt.Errorf("value exceeds %s range: %d", label, n)
		}
	} else {
		// limit doesn't fit in int (only possible on 32-bit systems with uint32)
		// but n (int) can't exceed limit in this case, so this branch is safe
		if int64(n) > limit {
			return 0, fmt.Errorf("value exceeds %s range: %d", label, n)
		}
	}
	return T(n), nil
}
