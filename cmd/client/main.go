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
	"net/http"
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
	ensureTCPHasTarget(cfg)
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
	handleHTTPProtocol(cfg, runtime, tun, httpClient, bearer)
	handleTCPServeIncoming(cfg, runtime, tun, httpClient, bearer)
	handleUDPProtocol(cfg, runtime, enc, tun, authToken, httpClient, bearer)

	if cfg.WatchWS {
		fmt.Printf("\n🔌 Connecting to WebSocket for real-time updates...\n")
		ctrl.ConnectWebSocketWithAuth(httpClient, cfg.ServerURL, tun.ID, bearer, runtime)
	}
}

// handleHTTPProtocol delegates to tunnel package and TCP data-plane
func handleHTTPProtocol(cfg *config.Config, runtime config.RuntimeSettings, tun *ctrl.Response, httpClient *http.Client, bearer string) {
	if isHTTPProtocol(cfg.Protocol) {
		reporter := dp.NewBackendStateReporter()
		errCh := make(chan error, 1)
		tunnelDeletedCh := make(chan struct{})
		go func() {
			errCh <- dp.StartDataPlaneServeIncoming(cfg.ServerURL, tun.ID, runtime, reporter)
		}()
		go ctrl.RunFallbackLifecyclePoller(httpClient, cfg.ServerURL, tun.ID, bearer, func() { close(tunnelDeletedCh) }, runtime.WatchInterval)

		ctrl.PrintHTTPHints(cfg.ServerURL, tun)
		fmt.Println("💡 Tip: If you see 'Backend unreachable', start your backend on the target address.")
		fmt.Println("\n🔌 Serving HTTP over data-plane. Press Ctrl+C to stop.")
		sigc := make(chan os.Signal, 1)
		signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
		select {
		case <-sigc:
			return
		case <-tunnelDeletedCh:
			os.Exit(0)
		case err := <-errCh:
			if err != nil {
				fmt.Printf("❌ Data-plane serve stopped: %v\n", err)
				ctrl.DeleteTunnelWithClient(cfg.ServerURL, tun.ID, httpClient, bearer)
				os.Exit(1)
			}
		}
	}
}

// handleTCPServeIncoming is the default TCP mode: serve incoming streams from server, dial local backend.
func handleTCPServeIncoming(cfg *config.Config, runtime config.RuntimeSettings, tun *ctrl.Response, httpClient *http.Client, bearer string) {
	if cfg.Protocol != "tcp" {
		return
	}
	reporter := dp.NewBackendStateReporter()
	errCh := make(chan error, 1)
	tunnelDeletedCh := make(chan struct{})
	go func() {
		errCh <- dp.StartDataPlaneServeIncoming(cfg.ServerURL, tun.ID, runtime, reporter)
	}()
	go ctrl.RunFallbackLifecyclePoller(httpClient, cfg.ServerURL, tun.ID, bearer, func() { close(tunnelDeletedCh) }, runtime.WatchInterval)
	log.Printf("INFO: TCP expose-local mode active; backend target %s", cfg.TargetAddr)
	fmt.Printf("\n🔌 Serving TCP over data-plane (expose-local). Backend: %s\n", cfg.TargetAddr)
	fmt.Println("💡 Tip: If you see 'Backend unreachable', start your backend on the target address.")
	fmt.Println("\n🔌 Press Ctrl+C to stop.")
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sigc:
		return
	case <-tunnelDeletedCh:
		os.Exit(0)
	case err := <-errCh:
		if err != nil {
			fmt.Printf("❌ Data-plane serve stopped: %v\n", err)
			ctrl.DeleteTunnelWithClient(cfg.ServerURL, tun.ID, httpClient, bearer)
			os.Exit(1)
		}
	}
}

// handleUDPProtocol delegates to UDP, QUIC, and DTLS packages
func handleUDPProtocol(cfg *config.Config, runtime config.RuntimeSettings, enc config.EncryptionSettings, tun *ctrl.Response, authToken string, httpClient *http.Client, bearer string) {
	if cfg.Protocol != "udp" {
		return
	}
	if cfg.UDPListen == "" || cfg.UDPDst == "" {
		log.Fatalf("for UDP mode, both --udp-listen and --udp-dst are required")
	}

	go ctrl.RunFallbackLifecyclePoller(httpClient, cfg.ServerURL, tun.ID, bearer, func() { os.Exit(0) }, runtime.WatchInterval)
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
	runUDPStrategy(strategy, cfg.ServerURL, tun.ID, httpClient, bearer)
}

func isHTTPProtocol(value string) bool {
	return value == protoHTTP || value == protoHTTPS
}

func ensureHTTPHasTarget(cfg *config.Config) {
	if isHTTPProtocol(cfg.Protocol) && cfg.TargetAddr == "" {
		log.Fatal("Target address is required (e.g. 127.0.0.1:8000)")
	}
}

func ensureTCPHasTarget(cfg *config.Config) {
	if cfg.Protocol != "tcp" {
		return
	}
	if cfg.TargetAddr == "" {
		log.Fatal("Target address is required for TCP expose-local mode (e.g. 127.0.0.1:5433)")
	}
}

// --- UDP strategy helpers ----------------------------------------------------

func runUDPStrategy(strategy dp.Strategy, serverURL, tunnelID string, httpClient *http.Client, bearer string) {
	fmt.Println(strategy.RunningMessage)
	if err := strategy.Run(); err != nil {
		ctrl.DeleteTunnelWithClient(serverURL, tunnelID, httpClient, bearer)
		log.Fatalf("%s: %v", strategy.ErrLabel, err)
	}
}
