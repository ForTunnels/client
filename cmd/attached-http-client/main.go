// Command attached-http-client is a test-only process fixture that connects the
// production HTTP data plane to an already-created tunnel. It deliberately has
// no discovery or creation behavior: the owning integration harness supplies a
// tunnel that it created through the assembled server API.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/fortunnels/client/internal/config"
	dp "github.com/fortunnels/client/internal/dataplane"
	protocolv1 "github.com/fortunnels/client/shared/protocol/v1"
)

func main() {
	serverURL := flag.String("server", "", "assembled server base URL")
	tunnelID := flag.String("tunnel-id", "", "existing tunnel ID")
	localAddr := flag.String("local", "", "local HTTP backend address")
	userID := flag.String("user", "", "authenticated development user")
	dpAuthToken := flag.String("dp-auth-token", "", "data-plane authentication token")
	flag.Parse()

	if strings.TrimSpace(*serverURL) == "" || strings.TrimSpace(*tunnelID) == "" ||
		strings.TrimSpace(*localAddr) == "" || strings.TrimSpace(*userID) == "" {
		fmt.Fprintln(os.Stderr, "server, tunnel-id, local, and user are required")
		os.Exit(2)
	}
	if err := verifyTunnelOwnership(*serverURL, *tunnelID, *userID); err != nil {
		fmt.Fprintf(os.Stderr, "verify tunnel ownership: %v\n", err)
		os.Exit(1)
	}

	runtime := config.RuntimeSettings{
		PingInterval:          200 * time.Millisecond,
		PingTimeout:           2 * time.Second,
		SmuxKeepAliveInterval: 500 * time.Millisecond,
		SmuxKeepAliveTimeout:  2 * time.Second,
		WatchInterval:         200 * time.Millisecond,
	}
	fmt.Printf("ATTACHING tunnel=%s local=%s\n", *tunnelID, *localAddr)
	reporter := dp.NewBackendStateReporter("http")
	if err := dp.StartDataPlaneServeIncoming(*serverURL, *tunnelID, runtime, reporter, *dpAuthToken); err != nil {
		fmt.Fprintf(os.Stderr, "attached HTTP data plane stopped: %v\n", err)
		os.Exit(1)
	}
}

func verifyTunnelOwnership(serverURL, tunnelID, userID string) error {
	endpoint := strings.TrimRight(serverURL, "/") + "/api/tunnels?id=" + url.QueryEscape(tunnelID)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("X-User-ID", userID)
	req.Header.Set("X-User-Role", "user")
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned %s", resp.Status)
	}
	var payload protocolv1.TunnelListResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return err
	}
	if !payload.Exists || len(payload.Tunnels) != 1 || payload.Tunnels[0].ID != tunnelID {
		return fmt.Errorf("owner-scoped tunnel lookup did not return requested tunnel")
	}
	return nil
}
