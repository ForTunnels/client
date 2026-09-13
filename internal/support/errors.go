// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package support

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"syscall"
	"unicode"
)

// HandleTunnelCreationError formats a user-friendly error for tunnel creation failures.
// Returns an error for the caller to handle (e.g. main exits); does not call os.Exit.
func HandleTunnelCreationError(err error, serverURL string, tunnelContext ...string) error {
	protocol, target := tunnelCreationContext(tunnelContext)
	if isAuthenticationRequired(err) && requiresSignedInAccount(protocol) {
		return accountRequiredTunnelError(protocol, target, serverURL)
	}
	if IsConnRefused(err) || IsDialTimeout(err) {
		return fmt.Errorf("❌ Unable to connect to server: %s\n   Make sure the server is running. Hint: make run-dev", serverURL)
	}
	if err != nil {
		return fmt.Errorf("❌ Failed to create tunnel: %w", err)
	}
	return fmt.Errorf("❌ Failed to create tunnel: unknown error")
}

func isAuthenticationRequired(err error) bool {
	if err == nil {
		return false
	}
	type codedError interface{ ErrorCode() string }
	var coded codedError
	if errors.As(err, &coded) && coded.ErrorCode() == "authentication_required" {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "authentication_required")
}

func accountRequiredTunnelError(protocol, target, serverURL string) error {
	canonical := strings.ToLower(strings.TrimSpace(protocol))
	label := strings.ToUpper(canonical)
	port := tunnelTargetPort(target)
	loginURL := configuredLoginURL(serverURL)
	return fmt.Errorf(
		"%s tunnels require a signed-in account. A Free account is sufficient. "+
			"Sign in with -login and -pass-stdin, then run this command again. "+
			"Anonymous mode is available for HTTP/HTTPS only.\n"+
			"   Example: fortunnels %s %s -login YOUR_LOGIN -pass-stdin\n"+
			"   Sign in or register: %s",
		label, canonical, port, loginURL,
	)
}

func tunnelCreationContext(values []string) (protocol, target string) {
	if len(values) > 0 {
		protocol = values[0]
	}
	if len(values) > 1 {
		target = values[1]
	}
	return protocol, target
}

func requiresSignedInAccount(protocol string) bool {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "tcp", "udp":
		return true
	default:
		return false
	}
}

func tunnelTargetPort(target string) string {
	if _, port, err := net.SplitHostPort(strings.TrimSpace(target)); err == nil && port != "" {
		return port
	}
	if target = strings.TrimSpace(target); target != "" && !strings.Contains(target, ":") {
		return target
	}
	return "PORT"
}

func configuredLoginURL(serverURL string) string {
	safe := sanitizeTerminalText(serverURL)
	u, err := url.Parse(safe)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return safe
	}
	u.Path = "/login"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func sanitizeTerminalText(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
}

// isConnRefused returns true if error indicates connection refused
func IsConnRefused(err error) bool {
	if err == nil {
		return false
	}
	var uerr *url.Error
	if errors.As(err, &uerr) {
		if IsConnRefused(uerr.Err) {
			return true
		}
	}
	var op *net.OpError
	if errors.As(err, &op) {
		var se *os.SyscallError
		if errors.As(op.Err, &se) {
			return errors.Is(se.Err, syscall.ECONNREFUSED)
		}
	}
	return strings.Contains(strings.ToLower(err.Error()), "connection refused")
}

// isDialTimeout returns true if error indicates dial timeout
func IsDialTimeout(err error) bool {
	if err == nil {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "timeout")
}
