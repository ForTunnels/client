// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package main

import (
	"fmt"
	"os"

	"github.com/fortunnels/client/internal/config"
)

func runConfigCommand(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: fortunnels config <add-authtoken|check>")
		return 2
	}
	switch args[0] {
	case "add-authtoken":
		return runConfigAddAuthtoken(args[1:])
	case "check":
		return runConfigCheck()
	default:
		fmt.Fprintf(os.Stderr, "unknown config command: %s\n", args[0])
		return 2
	}
}

func runConfigAddAuthtoken(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: fortunnels config add-authtoken <token>")
		return 2
	}
	path, err := config.DefaultConfigPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		return 1
	}
	if err := config.SaveAuthtoken(path, args[0]); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		return 1
	}
	fmt.Printf("Authtoken saved to %s\n", path)
	return 0
}

func runConfigCheck() int {
	path, err := config.DefaultConfigPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		return 1
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "No configuration file at %s\n", path)
			return 1
		}
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		return 1
	}
	fc, err := config.LoadFileConfig(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		return 1
	}
	if err := config.ValidateFileConfig(fc); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		return 1
	}
	fmt.Printf("Valid configuration file at %s\n", path)
	return 0
}
