# ForTunnels Client

[![Go Version](https://img.shields.io/badge/go-1.25.3-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/license-Proprietary-red.svg)](LICENSE)

Standalone CLI client for creating secure tunnels through [ForTunnels](https://fortunnels.ru). Expose local HTTP/HTTPS, TCP, and UDP services to the internet in seconds.

## Overview

ForTunnels Client is a command-line tool that connects to the ForTunnels server at `https://fortunnels.ru` and creates a public URL for your local service.

### Features

- **HTTP/HTTPS tunnels** - forward web services with automatic routing
- **TCP tunnels** - forward any TCP protocol (SSH, databases, etc.)
- **UDP tunnels** - support for UDP protocols (DNS, syslog, etc.)
- **Multiple transports** - WebSocket/smux, QUIC, DTLS
- **Encryption** - optional client-side stream encryption (PSK)
- **Monitoring** - tunnel status via WebSocket
- **Cross-platform** - Linux, macOS, Windows

## Requirements

- Go 1.22 or newer (for building from source)
- `make` (optional, for convenience targets)

## Installation

### Build from source

```bash
git clone https://github.com/fortunnels/client.git
cd client
go build -o ./bin/client ./cmd/client
# On Windows: go build -o ./bin/client.msi ./cmd/client
```

Or via `make`:

```bash
make build          # binary in ./bin/client (includes checks)
make build-fast     # without tests/coverage
```

On Windows (or when cross-compiling with `GOOS=windows`), the binary is produced as `bin/client.msi` instead of `bin/client`.

The `BIN_DIR` environment variable controls the output directory:

```bash
make build BIN_DIR=../bin
```

### Download binaries

Binaries for common platforms are available in [releases](https://github.com/fortunnels/client/releases).

## Quick start

### HTTP tunnel

Create a tunnel to a local web server on port 8000:

```bash
./bin/client 8000
```

After the tunnel is created you will receive a public URL like `https://fortunnels.ru/t/{tunnel-id}/`.
Host-based routing is also available, for example: `https://{subdomain}.fortunnels.ru`
(where `{subdomain}` is a random identifier, not the tunnel ID).

### HTTPS tunnel

```bash
./bin/client -local 127.0.0.1:8443 -protocol https
```

### TCP tunnel (SSH)

Forward SSH through a tunnel:

```bash
./bin/client -protocol tcp -local 127.0.0.1:22 -dst localhost:3333 -listen :4000
```

Now you can connect to SSH through the tunnel:

```bash
ssh -p 4000 user@localhost
```

### UDP tunnel (DNS)

```bash
./bin/client -protocol udp -udp-listen :5353 -udp-dst 127.0.0.1:53
```

## CLI options reference

### General options

- `-allow-insecure-http` - allow insecure HTTP for non-local addresses (not recommended)
- `-local` - local service address to forward (e.g. `127.0.0.1:8000`)
- `-protocol http|https|tcp|udp` - tunnel protocol
- `-user` - user identifier (for audit/quotas, default: `default`)
- `-dp ws|quic|dtls` - data-plane transport (default: `ws`)

### Execution mode

**Default:** all tunnels run in blocking mode and stay active until Ctrl+C.

### TCP mode

- `-dst host:port` - server-side target address for TCP forwarding/testing
- `-parallel N` - number of parallel smux streams for TCP tests/load
- `-listen :PORT` - local TCP listen address; incoming connections are proxied via data-plane to `-dst`
- `-backoff-initial` - initial reconnect delay in listen mode (sec, default: 1)
- `-backoff-max` - max reconnect delay in listen mode (sec, default: 30)

### UDP mode

- `-udp-listen :PORT` - local UDP listen address (e.g. `:5353`)
- `-udp-dst host:port` - server-side UDP destination (e.g. `127.0.0.1:53`)

### Reliability and monitoring

- `-ping-interval` - WebSocket ping interval (default: `30s`)
- `-ping-timeout` - ping write timeout (default: `10s`)
- `-smux-keepalive-interval` - smux keepalive interval (default: `25s`)
- `-smux-keepalive-timeout` - smux keepalive timeout (default: `60s`)
- `-watch` - tunnel monitoring mode (subscription/polling)
- `-watch-interval` - HTTP poll interval after WS subscription (default: `10s`)

### Encryption

- `-encrypt` - enable client-side stream encryption (PSK) over data-plane
- `-psk` - pre-shared key (required with `-encrypt`)
- `-psk-file` - read PSK from a file
- `-psk-stdin` - read PSK from stdin

**Note:** When using `-encrypt`, you must provide a non-empty `-psk`.

### Authentication notes

- `-login` - login for server authentication
- `-password` - password for server authentication
- `-password-file` - read password from a file
- `-password-stdin` - read password from stdin
- `-token` - Bearer JWT token for API authorization
- `-token-file` - read token from a file
- `-token-stdin` - read token from stdin
- `-dp-auth-token` - ready data-plane auth token (hex)
- `-dp-auth-token-file` - read data-plane token from a file
- `-dp-auth-token-stdin` - read data-plane token from stdin
- `-dp-auth-secret` - secret for computing token (HMAC-SHA256 over `tunnel_id`)
- `-dp-auth-secret-file` - read data-plane secret from a file
- `-dp-auth-secret-stdin` - read data-plane secret from stdin

### Positional arguments

Short forms are supported:

- `client 8000` - HTTP tunnel to `127.0.0.1:8000`
- `client http 8000` - explicit protocol
- `client tcp 22` - TCP tunnel to `127.0.0.1:22`

## Security

### TLS/HTTPS

- All connections to the server use HTTPS (TLS 1.2+)
- Server certificate validation is enabled by default
- Auto-configuration is used for local development (`localhost`/`127.0.0.1`)
- HTTP for non-local addresses is blocked without `-allow-insecure-http`

### Stream encryption

The client supports optional encryption over the data-plane:

- XChaCha20-Poly1305 (AEAD)
- Key derived from PSK and tunnel ID: `SHA256(PSK || tunnel_id)`
- Enable: `-encrypt -psk "your-secret-key"`

**Recommendations:**

- Use long random keys (at least 32 bytes)
- Do not reuse a PSK across tunnels
- Store PSK in environment variables, files, or secret managers

### Authentication

- Authentication via login/password or JWT token
- Tokens are passed via `Authorization: Bearer <token>`
- Data-plane supports additional HMAC-based authentication
- Prefer `*-file` or `*-stdin` for sensitive data

## Usage examples

### Webhooks

Test cloud webhooks against a local server:

```bash
./bin/client -local 127.0.0.1:3000 -protocol http
```

Use the public URL to configure the webhook.

### Demos

Show prototypes to customers:

```bash
./bin/client -local 127.0.0.1:8080 -protocol http
```

### SSH access

```bash
./bin/client -protocol tcp -local 127.0.0.1:22 -dst localhost:3333 -listen :4000
```

### UDP (DNS)

```bash
./bin/client -protocol udp -udp-listen :5353 -udp-dst 127.0.0.1:53
```

### With monitoring

```bash
./bin/client 8000 -watch -watch-interval 5s
```

### With encryption

```bash
./bin/client 8000 -encrypt -psk "$(openssl rand -hex 32)"
```

### QUIC transport

```bash
./bin/client -protocol tcp -dp quic -dst localhost:3333
```

### DTLS transport (UDP)

```bash
./bin/client -protocol udp -dp dtls -udp-listen :5353 -udp-dst 127.0.0.1:53
```

## Troubleshooting

### Unable to connect to the server

**Problem:** `Unable to connect to server`

**Fixes:**

- Check that the ForTunnels server is reachable: `curl https://fortunnels.ru/api/health`
- Check network settings and firewall

### Tunnel is not created

**Problem:** `Failed to create tunnel`

**Fixes:**

- Check authentication: use `-login`/`-password` or `-token`
- Ensure the local service is reachable: `curl http://127.0.0.1:8000`
- Check address format: must be `host:port` (e.g. `127.0.0.1:8000`)

### Empty PSK during encryption

**Problem:** `empty PSK`

**Fix:** When using `-encrypt`, provide a non-empty `-psk`:

```bash
./bin/client 8000 -encrypt -psk "your-secret-key-here"
```

### Connection timeouts

**Problem:** connections drop

**Fixes:**

- Increase timeouts: `-ping-timeout 30s`, `-smux-keepalive-timeout 120s`
- Check internet stability
- Use `-watch` to monitor tunnel state

### UDP does not work

**Problem:** UDP packets do not pass

**Fixes:**

- Ensure both flags are set: `-udp-listen` and `-udp-dst`
- Check that UDP is enabled on the server
- Try a different transport: `-dp quic` or `-dp dtls`

## Project structure

```text
client/
|-- cmd/client/          # CLI entrypoint
|-- internal/
|   |-- auth/            # Authentication
|   |-- config/          # Configuration and validation
|   |-- control/         # Control-plane operations
|   |-- dataplane/       # Data-plane transports (WS, QUIC, DTLS)
|   |-- security/        # Encryption (PSK)
|   `-- support/         # Utilities and error handling
|-- shared/
|   `-- wsconn/          # WebSocket adapter for smux
|-- Makefile             # Build/test/release targets
|-- go.mod               # Go module
`-- .golangci.yml        # Linter settings
```

## Code checks

Project uses custom linter configuration (`.golangci.yml`) tuned for CLI apps:

- `make check` - full checks (gofmt, go vet, tests, golangci-lint, govulncheck, staticcheck, ineffassign, misspell, gocyclo)
- All checks run automatically on `make build`

Strictness highlights:

- Cyclomatic complexity `gocyclo` limited to 12
- `funlen` max 80 lines and 60 statements per function
- `lll` max line length 200
- Duplicate finder (`dupl`) triggers at 50 tokens
- Any `//nolint` must include an explanation and list of linters

## Supported platforms

- Linux (amd64, arm64)
- macOS (amd64, arm64)
- Windows (amd64, arm64, x86)

## License

Proprietary License - see [LICENSE](LICENSE) for details.
This software is proprietary and may not be reused in other projects.

## Links

- [ForTunnels](https://fortunnels.ru) - official site
