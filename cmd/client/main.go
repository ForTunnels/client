// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package main

// Command client provides a CLI to create tunnels and test data-plane.
// Modes:
// - HTTP/HTTPS: creates control-plane tunnel and prints usage hints
// - TCP test: establishes WS→smux session and sends parallel echo messages
// - TCP listen: accepts local connections and forwards via smux streams

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
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
	protocolv1 "github.com/fortunnels/client/shared/protocol/v1"
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

	if len(os.Args) > 1 && os.Args[1] == "config" {
		os.Exit(runConfigCommand(os.Args[2:]))
	}

	if len(os.Args) > 1 && os.Args[1] == "webhook" {
		os.Exit(runWebhookCommand(os.Args[2:]))
	}

	cfg, err := parseConfig()
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	if err := runClientWorkflow(cfg); err != nil {
		writeWorkflowError(os.Stderr, err)
		os.Exit(1)
	}
}

func writeWorkflowError(output io.Writer, err error) {
	if err == nil || ctrl.IsReportedTerminalError(err) {
		return
	}
	fmt.Fprintf(output, "%v\n", err)
}

func parseConfig() (*config.Config, error) {
	config.SetDefaultServerURL(defaultServerURL)
	cfg, err := config.Parse()
	if err != nil {
		return nil, err
	}
	if err := config.Validate(cfg); err != nil {
		fmt.Println("❌", err.Error())
		os.Exit(2)
	}
	if err := ensureHTTPHasTarget(cfg); err != nil {
		return nil, err
	}
	if err := ensureTCPHasTarget(cfg); err != nil {
		return nil, err
	}
	if err := ensureUDPHasTarget(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func runClientWorkflow(cfg *config.Config) error {
	fmt.Printf("Creating tunnel for %s://%s\n", cfg.Protocol, cfg.TargetAddr)
	fmt.Printf("Connecting to server: %s\n", cfg.ServerURL)

	httpClient, bearer, csrf, err := auth.SetupAuthentication(cfg)
	if err != nil {
		return fmt.Errorf("❌ Authentication failed: %w", err)
	}

	tun, err := ctrl.CreateTunnelWithClient(
		cfg.ServerURL,
		cfg.TargetAddr,
		cfg.Protocol,
		cfg.UserID,
		httpClient,
		bearer,
		csrf,
	)
	if err != nil {
		if authErr := auth.MapCreateTunnelAuthError(err, bearer, cfg.TokenFromConfigFile, cfg.Protocol); authErr != nil {
			return fmt.Errorf("❌ Authentication failed: %w", authErr)
		}
		return clierrors.HandleTunnelCreationError(err, cfg.ServerURL, cfg.Protocol, cfg.TargetAddr)
	}

	if err := auth.CheckBearerNotRejectedAsGuest(bearer, cfg.TokenFromConfigFile, auth.TunnelGuestSignals{
		IsGuest: tun.IsGuest,
		UserID:  tun.UserID,
	}, cfg.Protocol); err != nil {
		// Guest tunnel was created without valid auth; omit rejected bearer on cleanup.
		ctrl.DeleteTunnelWithClient(cfg.ServerURL, tun.ID, httpClient, "", csrf)
		return fmt.Errorf("❌ Authentication failed: %w", err)
	}

	runtime := cfg.RuntimeSettings()
	enc := cfg.EncryptionSettings()
	authToken := auth.ComputeDataPlaneAuthWithPSK(tun.ID, cfg.DPAuthToken, cfg.DPAuthSecret, cfg.PSK, enc.Enabled)

	ctrl.PrintTunnelInfo(cfg.ServerURL, tun)
	loginUsed := strings.TrimSpace(cfg.Login) != "" && strings.TrimSpace(cfg.Password) != ""
	ctrl.WarnGuestTunnelWithLogin(loginUsed, tun)
	serveCtx, cancelServe := context.WithCancel(context.Background())
	defer cancelServe()
	terminal := ctrl.NewTerminalCoordinator(nil, cancelServe)
	go ctrl.RunFallbackLifecyclePollerWithReasonContext(serveCtx, httpClient, cfg.ServerURL, tun.ID, bearer, terminal.Signal, runtime.WatchInterval)
	if cfg.WatchWS {
		go ctrl.RunLifecycleWatch(serveCtx, httpClient, cfg.ServerURL, tun.ID, bearer, runtime, terminal.Signal)
	}

	if err := handleHTTPProtocol(serveCtx, terminal, cfg, runtime, tun, httpClient, bearer, csrf, authToken); err != nil {
		return err
	}
	if err := handleTCPServeIncoming(serveCtx, terminal, cfg, runtime, tun, httpClient, bearer, csrf, authToken); err != nil {
		return err
	}
	if err := handleUDPProtocol(serveCtx, terminal, cfg, runtime, enc, tun, authToken, httpClient, bearer, csrf); err != nil {
		return err
	}
	return nil
}

// handleHTTPProtocol delegates to tunnel package and TCP data-plane
func handleHTTPProtocol(
	ctx context.Context,
	terminal *ctrl.TerminalCoordinator,
	cfg *config.Config,
	runtime config.RuntimeSettings,
	tun *ctrl.Response,
	httpClient *http.Client,
	bearer, csrf, dpAuthToken string,
) error {
	if !isHTTPProtocol(cfg.Protocol) {
		return nil
	}
	reporter := dp.NewBackendStateReporter(cfg.Protocol)
	errCh := make(chan error, 1)
	go func() {
		errCh <- dp.StartDataPlaneServeIncomingContext(ctx, cfg.ServerURL, tun.ID, runtime, reporter, dpAuthToken, lifecycleSignal(terminal))
	}()

	fmt.Printf("💡 If the local %s backend is unavailable, the tunnel remains active; later incoming traffic retries it.\n", strings.ToUpper(cfg.Protocol))
	fmt.Println("\n🔌 Serving HTTP over data-plane. Press Ctrl+C to stop.")
	return waitForDataPlaneServe(terminal, errCh, cfg.ServerURL, tun.ID, httpClient, bearer, csrf)
}

// handleTCPServeIncoming is the default TCP mode: serve incoming streams from server, dial local backend.
func handleTCPServeIncoming(
	ctx context.Context,
	terminal *ctrl.TerminalCoordinator,
	cfg *config.Config,
	runtime config.RuntimeSettings,
	tun *ctrl.Response,
	httpClient *http.Client,
	bearer, csrf, dpAuthToken string,
) error {
	if !strings.EqualFold(cfg.Protocol, "tcp") {
		return nil
	}
	reporter := dp.NewBackendStateReporter(cfg.Protocol)
	errCh := make(chan error, 1)
	go func() {
		errCh <- dp.StartDataPlaneServeIncomingContext(ctx, cfg.ServerURL, tun.ID, runtime, reporter, dpAuthToken, lifecycleSignal(terminal))
	}()
	log.Printf("INFO: TCP expose-local mode active; backend target %s", cfg.TargetAddr)
	fmt.Printf("\n🔌 Serving TCP over data-plane (expose-local). Backend: %s\n", cfg.TargetAddr)
	fmt.Printf("💡 If the local %s backend is unavailable, the tunnel remains active; later incoming traffic retries it.\n", strings.ToUpper(cfg.Protocol))
	fmt.Println("\n🔌 Press Ctrl+C to stop.")
	return waitForDataPlaneServe(terminal, errCh, cfg.ServerURL, tun.ID, httpClient, bearer, csrf)
}

// handleUDPProtocol runs expose-local (default) or advanced reverse UDP proxy mode.
func handleUDPProtocol(
	ctx context.Context,
	terminal *ctrl.TerminalCoordinator,
	cfg *config.Config,
	runtime config.RuntimeSettings,
	enc config.EncryptionSettings,
	tun *ctrl.Response,
	authToken string,
	httpClient *http.Client,
	bearer, csrf string,
) error {
	if !strings.EqualFold(cfg.Protocol, "udp") {
		return nil
	}
	if config.IsUDPReverseMode(cfg) {
		strategy := dp.NewStrategy(
			strings.ToLower(cfg.DataPlane), cfg.ServerURL, tun.ID, authToken,
			cfg.UDPDst, cfg.UDPListen, runtime, enc,
		)
		return handleUDPReverseMode(ctx, terminal, strategy, cfg.ServerURL, tun.ID, httpClient, bearer, csrf)
	}
	return handleUDPExposeLocal(ctx, terminal, cfg, runtime, tun, authToken, httpClient, bearer, csrf)
}

func handleUDPExposeLocal(
	ctx context.Context,
	terminal *ctrl.TerminalCoordinator,
	cfg *config.Config,
	runtime config.RuntimeSettings,
	tun *ctrl.Response,
	authToken string,
	httpClient *http.Client,
	bearer, csrf string,
) error {
	errCh := make(chan error, 1)
	go func() {
		// A connected UDP socket does not establish peer reachability. Supplying
		// the stream-dial reporter here would announce a reachable backend before
		// any datagram response proves it, so UDP keeps its existing data path
		// without TCP-style up/down transition messages.
		errCh <- dp.StartDataPlaneServeIncomingUDPContext(
			ctx, cfg.ServerURL, tun.ID, runtime, nil, authToken, lifecycleSignal(terminal),
		)
	}()
	log.Printf("INFO: UDP expose-local mode active; backend target %s", cfg.TargetAddr)
	fmt.Printf("\n🔌 Serving UDP over data-plane (expose-local). Backend: %s\n", cfg.TargetAddr)
	fmt.Printf(
		"💡 If the local %s backend is unavailable, the tunnel remains active; later incoming traffic retries it.\n",
		strings.ToUpper(cfg.Protocol),
	)
	fmt.Println("\n🔌 Press Ctrl+C to stop.")
	return waitForDataPlaneServe(terminal, errCh, cfg.ServerURL, tun.ID, httpClient, bearer, csrf)
}

func handleUDPReverseMode(
	ctx context.Context,
	terminal *ctrl.TerminalCoordinator,
	strategy dp.Strategy,
	serverURL, tunnelID string,
	httpClient *http.Client,
	bearer, csrf string,
) error {
	errCh := make(chan error, 1)
	fmt.Print(strategy.Description)
	fmt.Println("\n🔌 Press Ctrl+C to stop.")
	go func() {
		errCh <- runUDPStrategyContext(ctx, strategy, serverURL, tunnelID, httpClient, bearer, csrf)
	}()
	return waitForProtocol(terminal, errCh, nil)
}

func waitForDataPlaneServe(
	terminal *ctrl.TerminalCoordinator,
	errCh <-chan error,
	serverURL, tunnelID string,
	httpClient *http.Client,
	bearer, csrf string,
) error {
	return waitForProtocol(terminal, errCh, func(err error) error {
		if err == nil {
			return nil
		}
		ctrl.DeleteTunnelWithClient(serverURL, tunnelID, httpClient, bearer, csrf)
		return fmt.Errorf("❌ Data-plane serve stopped: %w", err)
	})
}

func waitForProtocol(terminal *ctrl.TerminalCoordinator, errCh <-chan error, handleError func(error) error) error {
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigc)
	select {
	case <-sigc:
		return nil
	case <-terminal.Done():
		return terminal.ExitError()
	case err := <-errCh:
		if terminalErr := terminal.Err(); terminalErr != nil {
			return terminal.ExitError()
		}
		if handleError != nil {
			return handleError(err)
		}
		return err
	}
}

func lifecycleSignal(terminal *ctrl.TerminalCoordinator) func(protocolv1.LifecycleEventPayload) {
	return func(payload protocolv1.LifecycleEventPayload) {
		err := ctrl.TrafficQuotaErrorFromLifecycle(payload)
		terminal.Signal(err)
	}
}

func isHTTPProtocol(value string) bool {
	return value == protoHTTP || value == protoHTTPS
}

func ensureHTTPHasTarget(cfg *config.Config) error {
	if isHTTPProtocol(cfg.Protocol) && cfg.TargetAddr == "" {
		return fmt.Errorf("target address is required (e.g. 127.0.0.1:8000)")
	}
	return nil
}

func ensureTCPHasTarget(cfg *config.Config) error {
	if !strings.EqualFold(cfg.Protocol, "tcp") {
		return nil
	}
	if cfg.TargetAddr == "" {
		return fmt.Errorf("target address is required for TCP expose-local mode (e.g. 127.0.0.1:5433)")
	}
	return nil
}

func ensureUDPHasTarget(cfg *config.Config) error {
	if !config.IsUDPExposeLocalMode(cfg) {
		return nil
	}
	if cfg.TargetAddr == "" {
		return fmt.Errorf("target address is required for UDP expose-local mode (e.g. 127.0.0.1:9000)")
	}
	return nil
}

// --- UDP strategy helpers ----------------------------------------------------

func runUDPStrategyContext(ctx context.Context, strategy dp.Strategy, serverURL, tunnelID string, httpClient *http.Client, bearer, csrf string) error {
	fmt.Println(strategy.RunningMessage)
	if err := strategy.RunContext(ctx); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		ctrl.DeleteTunnelWithClient(serverURL, tunnelID, httpClient, bearer, csrf)
		return fmt.Errorf("%s: %w", strategy.ErrLabel, err)
	}
	return nil
}
