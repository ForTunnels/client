// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package config

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fortunnels/client/internal/support"
)

const (
	protoHTTP  = "http"
	protoHTTPS = "https"
	protoTCP   = "tcp"
	protoUDP   = "udp"
)

var defaultServerURL = "https://fortunnels.ru"

// SetDefaultServerURL allows overriding the default server URL (for ldflags compatibility).
func SetDefaultServerURL(value string) {
	if strings.TrimSpace(value) != "" {
		defaultServerURL = value
	}
}

// Config aggregates all CLI options after parsing.
type Config struct {
	Login                 string
	Password              string
	Token                 string
	ServerURL             string
	TargetAddr            string
	Protocol              string
	DataPlane             string
	UserID                string
	Dst                   string
	Parallel              int
	Listen                string
	BackoffInitial        time.Duration
	BackoffMax            time.Duration
	UDPListen             string
	UDPDst                string
	PingInterval          time.Duration
	PingTimeout           time.Duration
	SmuxInterval          time.Duration
	SmuxTimeout           time.Duration
	WatchInterval         time.Duration
	WatchWS               bool
	Encrypt               bool
	PSK                   string
	DPAuthToken           string
	DPAuthSecret          string
	TokenFile             string
	PasswordFile          string
	PSKFile               string
	DPAuthTokenFile       string
	DPAuthSecretFile      string
	TokenFromStdin        bool
	PasswordFromStdin     bool
	PSKFromStdin          bool
	DPAuthTokenFromStdin  bool
	DPAuthSecretFromStdin bool
	AllowInsecureHTTP     bool

	ServerFlagProvided       bool
	TokenFlagProvided        bool
	PasswordFlagProvided     bool
	PSKFlagProvided          bool
	DPAuthTokenFlagProvided  bool
	DPAuthSecretFlagProvided bool
}

// RuntimeSettings bundles frequently used timing knobs.
type RuntimeSettings struct {
	PingInterval          time.Duration
	PingTimeout           time.Duration
	SmuxKeepAliveInterval time.Duration
	SmuxKeepAliveTimeout  time.Duration
	WatchInterval         time.Duration
}

// EncryptionSettings describes stream encryption preferences.
type EncryptionSettings struct {
	Enabled bool
	PSK     string
}

// RuntimeSettings extracts timing configuration.
func (c *Config) RuntimeSettings() RuntimeSettings {
	return RuntimeSettings{
		PingInterval:          c.PingInterval,
		PingTimeout:           c.PingTimeout,
		SmuxKeepAliveInterval: c.SmuxInterval,
		SmuxKeepAliveTimeout:  c.SmuxTimeout,
		WatchInterval:         c.WatchInterval,
	}
}

// EncryptionSettings extracts encryption configuration.
func (c *Config) EncryptionSettings() EncryptionSettings {
	return EncryptionSettings{Enabled: c.Encrypt, PSK: c.PSK}
}

// Parse parses command-line flags and positional arguments into Config.
func Parse() (*Config, error) {
	normalizeArgs()

	cfg := defaultConfig()
	var durations durationFlags
	backoffInitialSec := 1
	backoffMaxSec := 30

	fs := flag.CommandLine
	fs.StringVar(&cfg.Login, "login", cfg.Login, "Login for server authentication")
	fs.StringVar(&cfg.Password, "pass", cfg.Password, "Password for server authentication")
	fs.StringVar(&cfg.Token, "token", cfg.Token, "Bearer JWT to authorize API calls")
	fs.StringVar(&cfg.PasswordFile, "pass-file", cfg.PasswordFile, "Read password from file")
	fs.StringVar(&cfg.TokenFile, "token-file", cfg.TokenFile, "Read bearer token from file")
	fs.BoolVar(&cfg.PasswordFromStdin, "pass-stdin", cfg.PasswordFromStdin, "Read password from stdin")
	fs.BoolVar(&cfg.TokenFromStdin, "token-stdin", cfg.TokenFromStdin, "Read bearer token from stdin")
	fs.StringVar(&cfg.ServerURL, "server", cfg.ServerURL, "Server URL")
	fs.BoolVar(&cfg.AllowInsecureHTTP, "allow-insecure-http", cfg.AllowInsecureHTTP, "Allow non-local HTTP server URL (unsafe)")
	fs.StringVar(&cfg.TargetAddr, "local", cfg.TargetAddr, "Target address to tunnel")
	fs.StringVar(&cfg.Protocol, "protocol", cfg.Protocol, "Protocol (http, https, tcp)")
	fs.StringVar(&cfg.DataPlane, "dp", cfg.DataPlane, "Data-plane transport (ws|quic|dtls)")
	fs.StringVar(&cfg.UserID, "user", cfg.UserID, "User ID")
	fs.StringVar(&cfg.Dst, "dst", cfg.Dst, "Destination for TCP test (server-side)")
	fs.IntVar(&cfg.Parallel, "parallel", cfg.Parallel, "Number of parallel streams for TCP test")
	fs.StringVar(&cfg.Listen, "listen", cfg.Listen, "Local TCP listen address (e.g. :4000) for client TCP mode")
	fs.IntVar(&backoffInitialSec, "backoff-initial", backoffInitialSec, "Initial reconnect backoff seconds")
	fs.IntVar(&backoffMaxSec, "backoff-max", backoffMaxSec, "Max reconnect backoff seconds")
	fs.StringVar(&cfg.UDPListen, "udp-listen", cfg.UDPListen, "Local UDP listen address (e.g. :5353) for client UDP mode")
	fs.StringVar(&cfg.UDPDst, "udp-dst", cfg.UDPDst, "Destination UDP address on server side (e.g. 127.0.0.1:53)")
	fs.StringVar(&durations.PingInterval, "ping-interval", "30s", "WebSocket ping interval")
	fs.StringVar(&durations.PingTimeout, "ping-timeout", "10s", "WebSocket ping write deadline")
	fs.StringVar(&durations.SmuxInterval, "smux-keepalive-interval", "25s", "smux keepalive interval")
	fs.StringVar(&durations.SmuxTimeout, "smux-keepalive-timeout", "60s", "smux keepalive timeout")
	fs.StringVar(&durations.WatchInterval, "watch-interval", "10s", "HTTP poll interval after WS subscription (fallback monitoring)")
	fs.BoolVar(&cfg.WatchWS, "watch", cfg.WatchWS, "Watch tunnel updates over WebSocket (runs until closed)")
	fs.BoolVar(&cfg.Encrypt, "encrypt", cfg.Encrypt, "Enable client-side stream encryption (PSK)")
	fs.StringVar(&cfg.PSK, "psk", cfg.PSK, "Pre-shared key for encryption")
	fs.StringVar(&cfg.PSKFile, "psk-file", cfg.PSKFile, "Read PSK from file")
	fs.BoolVar(&cfg.PSKFromStdin, "psk-stdin", cfg.PSKFromStdin, "Read PSK from stdin")
	fs.StringVar(&cfg.DPAuthToken, "dp-auth-token", cfg.DPAuthToken, "Precomputed data-plane auth token (hex)")
	fs.StringVar(&cfg.DPAuthSecret, "dp-auth-secret", cfg.DPAuthSecret, "Secret for computing data-plane auth token (HMAC-SHA256 over tunnel_id)")
	fs.StringVar(&cfg.DPAuthTokenFile, "dp-auth-token-file", cfg.DPAuthTokenFile, "Read data-plane auth token from file")
	fs.StringVar(&cfg.DPAuthSecretFile, "dp-auth-secret-file", cfg.DPAuthSecretFile, "Read data-plane auth secret from file")
	fs.BoolVar(&cfg.DPAuthTokenFromStdin, "dp-auth-token-stdin", cfg.DPAuthTokenFromStdin, "Read data-plane auth token from stdin")
	fs.BoolVar(&cfg.DPAuthSecretFromStdin, "dp-auth-secret-stdin", cfg.DPAuthSecretFromStdin, "Read data-plane auth secret from stdin")

	if err := fs.Parse(os.Args[1:]); err != nil {
		return nil, err
	}

	localProvided, serverProvided, protocolProvided, secretFlags := detectFlagOverrides()
	cfg.ServerFlagProvided = serverProvided
	cfg.TokenFlagProvided = secretFlags.token
	cfg.PasswordFlagProvided = secretFlags.password
	cfg.PSKFlagProvided = secretFlags.psk
	cfg.DPAuthTokenFlagProvided = secretFlags.dpAuthToken
	cfg.DPAuthSecretFlagProvided = secretFlags.dpAuthSecret

	remaining := fs.Args()
	processPositionalArgs(remaining, &cfg.Protocol, &cfg.TargetAddr, localProvided, protocolProvided)

	if err := applyDurationFlags(cfg, &durations); err != nil {
		return nil, err
	}
	cfg.BackoffInitial = time.Duration(backoffInitialSec) * time.Second
	cfg.BackoffMax = time.Duration(backoffMaxSec) * time.Second
	if cfg.WatchInterval < time.Second {
		cfg.WatchInterval = time.Second
	}

	if err := applySecretSources(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// GetProtocolConstants exposes protocol literals for other packages.
func GetProtocolConstants() (http, https, tcp, udp string) {
	return protoHTTP, protoHTTPS, protoTCP, protoUDP
}

// defaultConfig returns Config populated with CLI defaults.
func defaultConfig() *Config {
	return &Config{
		ServerURL:      support.GetDefaultServerURL(defaultServerURL),
		TargetAddr:     "localhost:3000",
		Protocol:       protoHTTP,
		DataPlane:      "ws",
		UserID:         "default",
		Dst:            "localhost:3333",
		Parallel:       1,
		BackoffInitial: time.Second,
		BackoffMax:     30 * time.Second,
		WatchInterval:  10 * time.Second,
		PSK:            "",
	}
}

type durationFlags struct {
	PingInterval  string
	PingTimeout   string
	SmuxInterval  string
	SmuxTimeout   string
	WatchInterval string
}

func applyDurationFlags(cfg *Config, d *durationFlags) error {
	parse := func(label, value string) (time.Duration, error) {
		dur, err := time.ParseDuration(value)
		if err != nil {
			return 0, fmt.Errorf("invalid %s: %w", label, err)
		}
		return dur, nil
	}

	var err error
	if cfg.PingInterval, err = parse("--ping-interval", d.PingInterval); err != nil {
		return err
	}
	if cfg.PingTimeout, err = parse("--ping-timeout", d.PingTimeout); err != nil {
		return err
	}
	if cfg.SmuxInterval, err = parse("--smux-keepalive-interval", d.SmuxInterval); err != nil {
		return err
	}
	if cfg.SmuxTimeout, err = parse("--smux-keepalive-timeout", d.SmuxTimeout); err != nil {
		return err
	}
	if cfg.WatchInterval, err = parse("--watch-interval", d.WatchInterval); err != nil {
		return err
	}
	return nil
}

func normalizeArgs() {
	if len(os.Args) <= 1 {
		return
	}
	var flagsOnly []string
	var positionals []string
	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		switch {
		case strings.HasPrefix(arg, "--"):
			normalized := "-" + strings.TrimPrefix(arg, "--")
			appendFlagArgument(&flagsOnly, os.Args, &i, normalized)
		case strings.HasPrefix(arg, "-"):
			appendFlagArgument(&flagsOnly, os.Args, &i, arg)
		default:
			positionals = append(positionals, arg)
		}
	}
	os.Args = append([]string{os.Args[0]}, append(flagsOnly, positionals...)...)
}

func appendFlagArgument(flags *[]string, args []string, idx *int, arg string) {
	*flags = append(*flags, arg)
	if needsInlineValue(arg, args, *idx) {
		*flags = append(*flags, args[*idx+1])
		*idx++
	}
}

func needsInlineValue(arg string, args []string, idx int) bool {
	return !strings.Contains(arg, "=") &&
		idx+1 < len(args) &&
		!strings.HasPrefix(args[idx+1], "-")
}

type secretFlagSet struct {
	token        bool
	password     bool
	psk          bool
	dpAuthToken  bool
	dpAuthSecret bool
}

func detectFlagOverrides() (localProvided, serverProvided, protocolProvided bool, secrets secretFlagSet) {
	flagProvided := func(name string) bool {
		for _, a := range os.Args[1:] {
			if a == "-"+name ||
				a == "--"+name ||
				strings.HasPrefix(a, "-"+name+"=") ||
				strings.HasPrefix(a, "--"+name+"=") {
				return true
			}
		}
		return false
	}
	secrets = secretFlagSet{
		token:        flagProvided("token"),
		password:     flagProvided("pass"),
		psk:          flagProvided("psk"),
		dpAuthToken:  flagProvided("dp-auth-token"),
		dpAuthSecret: flagProvided("dp-auth-secret"),
	}
	return flagProvided("local"), flagProvided("server"), flagProvided("protocol"), secrets
}

type secretSource struct {
	label     string
	value     *string
	file      *string
	fromStdin *bool
	envVar    string
}

func applySecretSources(cfg *Config) error {
	sources := []secretSource{
		{
			label:     "token",
			value:     &cfg.Token,
			file:      &cfg.TokenFile,
			fromStdin: &cfg.TokenFromStdin,
			envVar:    "FORTUNNELS_TOKEN",
		},
		{
			label:     "pass",
			value:     &cfg.Password,
			file:      &cfg.PasswordFile,
			fromStdin: &cfg.PasswordFromStdin,
			envVar:    "FORTUNNELS_PASSWORD",
		},
		{
			label:     "psk",
			value:     &cfg.PSK,
			file:      &cfg.PSKFile,
			fromStdin: &cfg.PSKFromStdin,
			envVar:    "FORTUNNELS_PSK",
		},
		{
			label:     "dp-auth-token",
			value:     &cfg.DPAuthToken,
			file:      &cfg.DPAuthTokenFile,
			fromStdin: &cfg.DPAuthTokenFromStdin,
			envVar:    "FORTUNNELS_DP_AUTH_TOKEN",
		},
		{
			label:     "dp-auth-secret",
			value:     &cfg.DPAuthSecret,
			file:      &cfg.DPAuthSecretFile,
			fromStdin: &cfg.DPAuthSecretFromStdin,
			envVar:    "FORTUNNELS_DP_AUTH_SECRET",
		},
	}

	if err := ensureSingleStdinSource(sources); err != nil {
		return err
	}
	for i := range sources {
		if err := applySecretSource(&sources[i]); err != nil {
			return err
		}
	}
	return nil
}

func ensureSingleStdinSource(sources []secretSource) error {
	var stdinFlags []string
	for _, src := range sources {
		if src.fromStdin != nil && *src.fromStdin {
			stdinFlags = append(stdinFlags, src.label)
		}
	}
	if len(stdinFlags) > 1 {
		return fmt.Errorf("only one --*-stdin option can be used at a time: %s", strings.Join(stdinFlags, ", "))
	}
	return nil
}

func applySecretSource(source *secretSource) error {
	if source == nil || source.value == nil {
		return nil
	}
	if *source.value != "" {
		return nil
	}
	if source.file != nil && *source.file != "" {
		secret, err := support.ReadSecretFile(*source.file)
		if err != nil {
			return err
		}
		*source.value = secret
		return nil
	}
	if source.fromStdin != nil && *source.fromStdin {
		secret, err := support.ReadSecretStdin(source.label)
		if err != nil {
			return err
		}
		*source.value = secret
		return nil
	}
	*source.value = support.GetEnvTrimmed(source.envVar)
	return nil
}

func processPositionalArgs(args []string, protocol, targetAddr *string, localFlagProvided, protocolFlagProvided bool) {
	switch len(args) {
	case 0:
		return
	case 1:
		handleSingleArg(args[0], protocol, targetAddr, localFlagProvided, protocolFlagProvided)
	default:
		handleProtocolAndAddrArgs(args[0], args[1], protocol, targetAddr, localFlagProvided, protocolFlagProvided)
	}
}

func handleSingleArg(arg string, protocol, targetAddr *string, localFlagProvided, protocolFlagProvided bool) {
	if p := support.ParsePort(arg); p != "" {
		setProtocolIfMissing(protocol, protocolFlagProvided, protoHTTP)
		setTargetIfMissing(targetAddr, localFlagProvided, "127.0.0.1:"+p)
		return
	}
	if support.LooksLikeHostPort(arg) {
		setProtocolIfMissing(protocol, protocolFlagProvided, protoHTTP)
		setTargetIfMissing(targetAddr, localFlagProvided, arg)
	}
}

func handleProtocolAndAddrArgs(protoArg, addrArg string, protocol, targetAddr *string, localFlagProvided, protocolFlagProvided bool) {
	argProto := strings.ToLower(protoArg)
	if !isSupportedProtocol(argProto) {
		return
	}
	setProtocolIfMissing(protocol, protocolFlagProvided, argProto)
	if p := support.ParsePort(addrArg); p != "" {
		setTargetIfMissing(targetAddr, localFlagProvided, "127.0.0.1:"+p)
		return
	}
	if support.LooksLikeHostPort(addrArg) {
		setTargetIfMissing(targetAddr, localFlagProvided, addrArg)
	}
}

func setProtocolIfMissing(protocol *string, provided bool, value string) {
	if !provided {
		*protocol = value
	}
}

func setTargetIfMissing(targetAddr *string, provided bool, value string) {
	if !provided {
		*targetAddr = value
	}
}

func isSupportedProtocol(p string) bool {
	switch p {
	case protoHTTP, protoHTTPS, protoTCP, protoUDP:
		return true
	default:
		return false
	}
}
