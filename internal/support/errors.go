// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package support

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"syscall"
)

// handleTunnelCreationError provides user-friendly error messages for tunnel creation failures.
func HandleTunnelCreationError(err error, serverURL string) {
	if IsConnRefused(err) || IsDialTimeout(err) {
		fmt.Println("❌ Unable to connect to server:", serverURL)
		fmt.Println("   Make sure the server is running. Hint: make run-dev")
		os.Exit(1)
	}
	if err != nil {
		fmt.Printf("❌ Failed to create tunnel: %v\n", err)
	} else {
		fmt.Println("❌ Failed to create tunnel: unknown error")
	}
	os.Exit(1)
}

// isConnRefused returns true if error indicates connection refused
func IsConnRefused(err error) bool {
	var uerr *url.Error
	if As(err, &uerr) {
		if IsConnRefused(uerr.Err) {
			return true
		}
	}
	var op *net.OpError
	if As(err, &op) {
		if se, ok := op.Err.(*os.SyscallError); ok {
			return se.Err == syscall.ECONNREFUSED
		}
		// sometimes wrapped directly
		if se, ok := op.Err.(*os.SyscallError); ok {
			return se.Err == syscall.ECONNREFUSED
		}
	}
	return strings.Contains(strings.ToLower(err.Error()), "connection refused")
}

// isDialTimeout returns true if error indicates dial timeout
func IsDialTimeout(err error) bool {
	var ne net.Error
	if As(err, &ne) && ne.Timeout() {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "timeout")
}

// As is a wrapper around errors.As for compatibility
func As(err error, target any) bool {
	switch t := target.(type) {
	case **url.Error:
		if uerr, ok := err.(*url.Error); ok {
			*t = uerr
			return true
		}
	case **net.OpError:
		if operr, ok := err.(*net.OpError); ok {
			*t = operr
			return true
		}
	case *net.Error:
		if nerr, ok := err.(net.Error); ok {
			*t = nerr
			return true
		}
	}
	return false
}
