// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package support

import (
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"testing"
)

func TestReadSecretStdinRedirected(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
		err   string
	}{
		{name: "no newline", input: "secret", want: "secret"},
		{name: "CRLF and whitespace", input: " \tsecret\r\n", want: "secret"},
		{name: "reads through EOF", input: "first\nsecond\n", want: "first\nsecond"},
		{name: "empty", err: "pass from stdin is empty"},
		{name: "whitespace", input: " \t\r\n", err: "pass from stdin is empty"},
		{name: "at limit", input: strings.Repeat("x", maxSecretSize), want: strings.Repeat("x", maxSecretSize)},
		{name: "over limit", input: strings.Repeat("x", maxSecretSize+1), err: "pass from stdin is too large"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, err := os.CreateTemp(t.TempDir(), "stdin")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if _, writeErr := file.WriteString(tt.input); writeErr != nil {
				t.Fatal(writeErr)
			}
			if _, seekErr := file.Seek(0, io.SeekStart); seekErr != nil {
				t.Fatal(seekErr)
			}
			previous := os.Stdin
			os.Stdin = file
			defer func() { os.Stdin = previous }()
			got, err := ReadSecretStdin("pass")
			if tt.err != "" {
				if err == nil || err.Error() != tt.err {
					t.Fatalf("error = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("secret length = %d, error = %v, want length %d", len(got), err, len(tt.want))
			}
		})
	}
}

func TestReadTerminalSecretLine(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
		err   string
	}{
		{name: "Enter", input: "secret\rmore", want: "secret"},
		{name: "newline", input: "secret\nmore", want: "secret"},
		{name: "empty Enter", input: "\r"},
		{name: "UTF8 backspace", input: "aé\x7fb\r", want: "ab"},
		{name: "empty backspace", input: "\x08secret\r", want: "secret"},
		{name: "clear line", input: "wrong\x15right\r", want: "right"},
		{name: "Ctrl D", input: "secret\x04", want: "secret"},
		{name: "Ctrl C", input: "secret\x03", err: "secret entry interrupted"},
		{name: "read error", input: "secret", err: "EOF"},
		{name: "at limit", input: strings.Repeat("x", maxSecretSize) + "\r", want: strings.Repeat("x", maxSecretSize)},
		{name: "overflow sentinel", input: strings.Repeat("x", maxSecretSize+10), want: strings.Repeat("x", maxSecretSize+1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readTerminalSecretLine(strings.NewReader(tt.input))
			if tt.err != "" {
				if err == nil || err.Error() != tt.err {
					t.Fatalf("error = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil || string(got) != tt.want {
				t.Fatalf("secret length = %d, error = %v, want length %d", len(got), err, len(tt.want))
			}
		})
	}
}

func TestReadTerminalSecretLineDrainsOverflow(t *testing.T) {
	input := strings.NewReader(strings.Repeat("x", maxSecretSize+100) + "\rnext-line")
	got, err := readTerminalSecretLine(input)
	if err != nil || len(got) != maxSecretSize+1 {
		t.Fatalf("length = %d, error = %v", len(got), err)
	}
	rest, err := io.ReadAll(input)
	if err != nil || string(rest) != "next-line" {
		t.Fatalf("overflow line was not fully consumed, remaining length = %d, error = %v", len(rest), err)
	}
}

func TestIsBenignCopyError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, true},
		{"EOF", io.EOF, true},
		{"UnexpectedEOF", io.ErrUnexpectedEOF, true},
		{"ErrClosedPipe", io.ErrClosedPipe, true},
		{"net.ErrClosed", net.ErrClosed, true},
		{"connection closed message", &net.OpError{Err: &os.SyscallError{Err: net.ErrClosed}}, true},
		{"broken pipe message", errors.New("broken pipe"), true},
		{"stream closed message", errors.New("stream closed"), true},
		{"arbitrary EOF text", errors.New("remote returned EOF while processing request"), false},
		{"real error", errors.New("permission denied"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsBenignCopyError(tt.err)
			if result != tt.expected {
				t.Errorf("IsBenignCopyError(%v) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

func TestToUint32Size(t *testing.T) {
	tests := []struct {
		name    string
		input   int
		wantErr bool
	}{
		{"valid small", 100, false},
		{"valid large", 1000000, false},
		{"zero", 0, false},
		{"negative", -1, true},
		{"max uint32", 4294967295, false},
		{"over max", 4294967296, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ToUint32Size(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ToUint32Size(%d) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && result != uint32(tt.input) {
				t.Errorf("ToUint32Size(%d) = %d, want %d", tt.input, result, tt.input)
			}
		})
	}
}

func TestGetEnvTrimmed(t *testing.T) {
	t.Setenv("TEST_ENV_TRIM", "  value  ")
	if got := GetEnvTrimmed("TEST_ENV_TRIM"); got != "value" {
		t.Fatalf("GetEnvTrimmed() = %q, want value", got)
	}
	t.Setenv("TEST_ENV_BLANK", "   ")
	if got := GetEnvTrimmed("TEST_ENV_BLANK"); got != "" {
		t.Fatalf("GetEnvTrimmed(blank) = %q, want empty", got)
	}
	if got := GetEnvTrimmed("TEST_ENV_MISSING_XYZ"); got != "" {
		t.Fatalf("GetEnvTrimmed(missing) = %q, want empty", got)
	}
}

func TestParsePort(t *testing.T) {
	if got := ParsePort("8080"); got != "8080" {
		t.Fatalf("ParsePort(8080) = %q", got)
	}
	if got := ParsePort(":9090"); got != "9090" {
		t.Fatalf("ParsePort(:9090) = %q", got)
	}
	if got := ParsePort(""); got != "" {
		t.Fatalf("ParsePort(empty) = %q", got)
	}
	if got := ParsePort("abc"); got != "" {
		t.Fatalf("ParsePort(abc) = %q", got)
	}
}

func TestLooksLikeHostPort(t *testing.T) {
	if !LooksLikeHostPort("127.0.0.1:8080") {
		t.Fatal("expected host:port")
	}
	if LooksLikeHostPort("8080") {
		t.Fatal("port only should be false")
	}
	if LooksLikeHostPort(":8080") {
		t.Fatal("missing host should be false")
	}
}

func TestReadSecretFile(t *testing.T) {
	path := t.TempDir() + "/secret"
	if err := os.WriteFile(path, []byte("  sekret \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSecretFile(path)
	if err != nil {
		t.Fatalf("ReadSecretFile: %v", err)
	}
	if got != "sekret" {
		t.Fatalf("ReadSecretFile = %q", got)
	}
	if _, err := ReadSecretFile(path + "-missing"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestToUint16Size(t *testing.T) {
	tests := []struct {
		name    string
		input   int
		wantErr bool
	}{
		{"valid small", 100, false},
		{"zero", 0, false},
		{"negative", -1, true},
		{"max uint16", 65535, false},
		{"over max", 65536, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ToUint16Size(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ToUint16Size(%d) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && result != uint16(tt.input) {
				t.Errorf("ToUint16Size(%d) = %d, want %d", tt.input, result, tt.input)
			}
		})
	}
}
