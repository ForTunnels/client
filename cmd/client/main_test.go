// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package main

import (
	"bytes"
	"flag"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/fortunnels/client/internal/config"
	ctrlTunnel "github.com/fortunnels/client/internal/control"
	"github.com/fortunnels/client/internal/support"
)

func TestParsePortAndLooksLikeHostPort(t *testing.T) {
	cases := []struct {
		in       string
		port     string
		hostPort string
	}{
		{"8000", "8000", ""},
		{":9000", "9000", ""},
		{"127.0.0.1:8080", "", "127.0.0.1:8080"},
		{"localhost:3000", "", "localhost:3000"},
		{"bad:value", "", ""},
		{"abc", "", ""},
	}
	for _, c := range cases {
		if p := support.ParsePort(c.in); p != c.port {
			t.Fatalf("parsePort(%q)=%q want %q", c.in, p, c.port)
		}
		if got := support.LooksLikeHostPort(c.in); (got && c.hostPort == "") || (!got && c.hostPort != "") {
			t.Fatalf("looksLikeHostPort(%q) unexpected result", c.in)
		}
	}
}

func TestPrintHTTPHints(t *testing.T) {
	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	srv := "http://127.0.0.1:8080"
	tun := &ctrlTunnel.Response{
		ID:         "tid",
		UserID:     100, // int64, not string
		TargetAddr: "127.0.0.1:8000",
		PublicURL:  "http://pub",
	}
	ctrlTunnel.PrintHTTPHints(srv, tun)

	_ = w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	out := buf.String()

	// Check that new guidance is present
	if !strings.Contains(out, "Default: stays running") {
		t.Fatalf("expected stay-by-default hint in output, got: %s", out)
	}
	if !strings.Contains(out, "/t/tid") {
		t.Fatalf("expected path-based example with tunnel ID, got: %s", out)
	}
}

func TestParseUsesEnvSecrets(t *testing.T) {
	oldArgs := os.Args
	oldFlag := flag.CommandLine
	defer func() {
		os.Args = oldArgs
		flag.CommandLine = oldFlag
	}()

	flag.CommandLine = flag.NewFlagSet("client", flag.ContinueOnError)
	os.Args = []string{"client"}
	t.Setenv("FORTUNNELS_TOKEN", "env-token")

	cfg, err := config.Parse()
	if err != nil {
		t.Fatalf("Parse() unexpected error: %v", err)
	}
	if cfg.Token != "env-token" {
		t.Fatalf("Token = %q, want %q", cfg.Token, "env-token")
	}
}
