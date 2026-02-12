// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package main

// Command client provides a CLI to create tunnels and test data-plane.
// Modes:
// - HTTP/HTTPS: creates control-plane tunnel and prints usage hints
// - TCP test: establishes WS→smux session and sends parallel echo messages
// - TCP listen: accepts local connections and forwards via smux streams

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/fortunnels/client/internal/auth"
	"github.com/fortunnels/client/internal/config"
	ctrl "github.com/fortunnels/client/internal/control"
	dp "github.com/fortunnels/client/internal/dataplane"
	clierrors "github.com/fortunnels/client/internal/support"
)

const (
	protoHTTP  = "http"
	protoHTTPS = "https"
)

var (
	defaultServerURL = "https://fortunnels.ru"
	version          = "dev" // Set via ldflags during build
)

func main() {
	// Check for version flag first
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v" || os.Args[1] == "version") {
		fmt.Printf("fortunnels-client %s\n", version)
		os.Exit(0)
	}

	cfg := parseConfigOrExit()
	runClientWorkflow(cfg)
}

func parseConfigOrExit() *config.Config {
	config.SetDefaultServerURL(defaultServerURL)
	cfg, err := config.Parse()
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		log.Fatal(err)
	}
	config.Validate(cfg)
	ensureHTTPHasTarget(cfg)
	return cfg
}

func runClientWorkflow(cfg *config.Config) {
	fmt.Printf("Creating tunnel for %s://%s\n", cfg.Protocol, cfg.TargetAddr)
	fmt.Printf("Connecting to server: %s\n", cfg.ServerURL)

	httpClient, bearer, err := auth.SetupAuthentication(cfg)
	if err != nil {
		fmt.Printf("❌ Authentication failed: %v\n", err)
		os.Exit(1)
	}

	tun, err := ctrl.CreateTunnelWithClient(
		cfg.ServerURL,
		cfg.TargetAddr,
		cfg.Protocol,
		cfg.UserID,
		httpClient,
		bearer,
	)
	if err != nil {
		clierrors.HandleTunnelCreationError(err, cfg.ServerURL)
	}

	runtime := cfg.RuntimeSettings()
	enc := cfg.EncryptionSettings()
	authToken := auth.ComputeDataPlaneAuth(tun.ID, cfg.DPAuthToken, cfg.DPAuthSecret)

	ctrl.PrintTunnelInfo(tun)
	handleHTTPProtocol(cfg, runtime, tun)
	handleTCPListenMode(cfg, runtime, enc, tun)
	handleTCPTestModes(cfg, runtime, enc, tun, authToken)
	handleUDPProtocol(cfg, runtime, enc, tun, authToken)

	if cfg.WatchWS {
		fmt.Printf("\n🔌 Connecting to WebSocket for real-time updates...\n")
		ctrl.ConnectWebSocket(cfg.ServerURL, tun.ID, runtime)
	}
}

// handleHTTPProtocol delegates to tunnel package and TCP data-plane
func handleHTTPProtocol(cfg *config.Config, runtime config.RuntimeSettings, tun *ctrl.Response) {
	if isHTTPProtocol(cfg.Protocol) {
		go func() {
			//nolint:errcheck // fire-and-forget background serve
			_ = dp.StartDataPlaneServeIncoming(cfg.ServerURL, tun.ID, runtime)
		}()
	}

	if isHTTPProtocol(cfg.Protocol) {
		ctrl.PrintHTTPHints(cfg.ServerURL, tun)
		fmt.Println("\n🔌 Serving HTTP over data-plane. Press Ctrl+C to stop.")
		sigc := make(chan os.Signal, 1)
		signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
		<-sigc
	}
}

// handleTCPListenMode delegates to TCP package
func handleTCPListenMode(cfg *config.Config, runtime config.RuntimeSettings, enc config.EncryptionSettings, tun *ctrl.Response) {
	if cfg.Protocol != "tcp" || cfg.Listen == "" {
		return
	}
	fmt.Printf("\n🔌 Listening on %s; forwarding over WS→smux to %s ...\n", cfg.Listen, cfg.Dst)
	if err := dp.StartDataPlaneServeListenReconnect(
		cfg.ServerURL,
		tun.ID,
		cfg.Dst,
		cfg.Listen,
		cfg.BackoffInitial,
		cfg.BackoffMax,
		runtime,
		enc,
	); err != nil {
		log.Fatalf("listen mode error: %v", err)
	}
}

// handleTCPTestModes delegates to TCP and QUIC packages
func handleTCPTestModes(cfg *config.Config, runtime config.RuntimeSettings, enc config.EncryptionSettings, tun *ctrl.Response, authToken string) {
	if cfg.Protocol != "tcp" {
		return
	}
	if cfg.DataPlane == "quic" {
		fmt.Printf("\n🔌 Establishing QUIC data-plane for TCP test to %s...\n", cfg.Dst)
		if err := dp.StartQUICDataPlaneTCP(
			cfg.ServerURL,
			tun.ID,
			authToken,
			cfg.Dst,
			cfg.Parallel,
		); err != nil {
			log.Fatalf("quic data-plane error: %v", err)
		}
		fmt.Printf("✅ TCP test (QUIC) completed\n")
		return
	}

	if cfg.Parallel <= 1 {
		fmt.Printf("\n🔌 Establishing data-plane (WS→smux) for TCP test to %s...\n", cfg.Dst)
		if err := dp.StartDataPlane(cfg.ServerURL, tun.ID, cfg.Dst, runtime, enc); err != nil {
			log.Fatalf("data-plane error: %v", err)
		}
		fmt.Printf("✅ TCP test completed\n")
		return
	}

	fmt.Printf(
		"\n🔌 Establishing data-plane (WS→smux) with %d parallel streams to %s...\n",
		cfg.Parallel,
		cfg.Dst,
	)
	if err := dp.StartDataPlaneParallel(cfg.ServerURL, tun.ID, cfg.Dst, cfg.Parallel, runtime, enc); err != nil {
		log.Fatalf("parallel data-plane error: %v", err)
	}
	fmt.Printf("✅ Parallel TCP test completed\n")
}

// handleUDPProtocol delegates to UDP, QUIC, and DTLS packages
func handleUDPProtocol(cfg *config.Config, runtime config.RuntimeSettings, enc config.EncryptionSettings, tun *ctrl.Response, authToken string) {
	if cfg.Protocol != "udp" {
		return
	}
	if cfg.UDPListen == "" || cfg.UDPDst == "" {
		log.Fatalf("for UDP mode, both --udp-listen and --udp-dst are required")
	}

	plane := strings.ToLower(cfg.DataPlane)

	strategy := dp.NewStrategy(
		plane,
		cfg.ServerURL,
		tun.ID,
		authToken,
		cfg.UDPDst,
		cfg.UDPListen,
		runtime,
		enc,
	)
	fmt.Print(strategy.Description)
	runUDPStrategy(strategy)
}

func isHTTPProtocol(value string) bool {
	return value == protoHTTP || value == protoHTTPS
}

func ensureHTTPHasTarget(cfg *config.Config) {
	if isHTTPProtocol(cfg.Protocol) && cfg.TargetAddr == "" {
		log.Fatal("Target address is required (e.g. 127.0.0.1:8000)")
	}
}

// --- UDP strategy helpers ----------------------------------------------------

func runUDPStrategy(strategy dp.Strategy) {
	fmt.Println(strategy.RunningMessage)
	if err := strategy.Run(); err != nil {
		log.Fatalf("%s: %v", strategy.ErrLabel, err)
	}
}
