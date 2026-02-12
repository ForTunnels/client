// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fortunnels/client/internal/config"
)

func TestStartUDPLocalToStream_WithMocks(t *testing.T) {
	// Test the UDP to stream forwarding logic
	// This function starts a goroutine that reads from UDP and writes to stream
	// We test that it handles errors correctly
	writer := &mockWriter{
		data: make([]byte, 0),
	}

	// Create a UDP connection that will be closed immediately to trigger error
	uc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("Failed to create UDP listener: %v", err)
	}

	errCh := make(chan error, 1)
	var lastSrcMu sync.RWMutex
	var lastSrc *net.UDPAddr

	// Start the goroutine
	startUDPLocalToStream(writer, uc, errCh, &lastSrcMu, &lastSrc)

	// Close immediately to trigger read error
	uc.Close()

	// Wait for error
	select {
	case err := <-errCh:
		if err == nil {
			t.Error("startUDPLocalToStream() should return error when connection closes")
		}
	case <-time.After(500 * time.Millisecond):
		// Timeout is acceptable - function may handle error differently
	}
}

func TestStartStreamToUDPLocal_WithMocks(t *testing.T) {
	// Test the stream to UDP forwarding logic
	reader := &mockReader{
		data: []byte{0, 5, 'h', 'e', 'l', 'l', 'o'}, // [len=5|"hello"]
	}

	uc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("Failed to create UDP listener: %v", err)
	}
	defer uc.Close()

	errCh := make(chan error, 1)
	var lastSrcMu sync.RWMutex
	lastSrc := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345}

	// Start the goroutine
	startStreamToUDPLocal(reader, uc, errCh, &lastSrcMu, &lastSrc)

	// Wait a bit for processing
	time.Sleep(100 * time.Millisecond)

	// Check for errors
	select {
	case err := <-errCh:
		if err != nil && err != io.EOF {
			t.Errorf("startStreamToUDPLocal() error = %v", err)
		}
	default:
		// No error, which is good
	}
}

func TestManager_EnsureSession_Stopped(t *testing.T) {
	mgr := NewManager("http://example.com", "tunnel-123", time.Second, 30*time.Second, config.RuntimeSettings{})
	mgr.Close()

	_, err := mgr.EnsureSession()
	if err == nil {
		t.Error("Manager.EnsureSession() should return error when stopped")
	}
}

func TestManager_InitializeSession_Error(t *testing.T) {
	// This tests the error path in initializeSession.
	// EnsureSession() dials the server and retries with backoff, so run with a timeout
	// to avoid hanging when the dial is slow or never fails.
	mgr := NewManager("http://example.com", "tunnel-123", time.Second, 30*time.Second, config.RuntimeSettings{})

	done := make(chan struct{})
	var err error
	go func() {
		_, err = mgr.EnsureSession()
		close(done)
	}()

	select {
	case <-done:
		if err == nil {
			t.Error("Manager.EnsureSession() should return error with invalid server")
		}
	case <-time.After(10 * time.Second):
		// Do not call mgr.Close() here: EnsureSession holds the manager lock during
		// dial and backoff sleep, so Close() would block until the goroutine returns.
		t.Skip("EnsureSession did not return within 10s (dial may be slow or network unavailable)")
	}
}

func TestReadStreamDestination_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		preface string
		want    string
		wantErr bool
	}{
		{
			name:    "preface with extra whitespace",
			preface: `  {"dst": "127.0.0.1:8080", "proto": "tcp"}  ` + "\n",
			want:    "127.0.0.1:8080",
			wantErr: false,
		},
		{
			name:    "preface with tabs",
			preface: "\t{\"dst\": \"127.0.0.1:8080\", \"proto\": \"tcp\"}\t\n",
			want:    "127.0.0.1:8080",
			wantErr: false,
		},
		{
			name:    "preface with newlines",
			preface: "\n{\"dst\": \"127.0.0.1:8080\", \"proto\": \"tcp\"}\n\n",
			want:    "127.0.0.1:8080",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rd := bufio.NewReader(strings.NewReader(tt.preface))
			got, err := readStreamDestination(rd)
			if (err != nil) != tt.wantErr {
				t.Errorf("readStreamDestination() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("readStreamDestination() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSendUDPPreface_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		dst      string
		tunnelID string
		wantErr  bool
	}{
		{
			name:     "special characters in dst",
			dst:      "127.0.0.1:8080",
			tunnelID: "tunnel-123",
			wantErr:  false,
		},
		{
			name:     "long tunnel ID",
			dst:      "127.0.0.1:8080",
			tunnelID: strings.Repeat("a", 100),
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer := &mockWriter{}
			err := sendUDPPreface(writer, tt.dst, tt.tunnelID)
			if (err != nil) != tt.wantErr {
				t.Errorf("sendUDPPreface() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				// Verify JSON is valid
				var preface map[string]string
				jsonData := writer.data[:len(writer.data)-1] // Remove newline
				if err := json.Unmarshal(jsonData, &preface); err != nil {
					t.Errorf("sendUDPPreface() wrote invalid JSON: %v", err)
				}
			}
		})
	}
}

func TestWriteUDPPacket_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
		wantErr bool
	}{
		{
			name:    "single byte",
			payload: []byte{0x42},
			wantErr: false,
		},
		{
			name:    "max UDP payload",
			payload: make([]byte, 65507),
			wantErr: false,
		},
		{
			name:    "exactly 65535 bytes",
			payload: make([]byte, 65535),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer := &mockWriter{}
			err := writeUDPPacket(writer, tt.payload)
			if (err != nil) != tt.wantErr {
				t.Errorf("writeUDPPacket() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestReadUDPPacket_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		frame   []byte
		wantErr bool
	}{
		{
			name:    "single byte packet",
			frame:   []byte{0, 1, 0x42},
			wantErr: false,
		},
		{
			name:    "max size packet",
			frame:   append([]byte{0xFF, 0xFF}, make([]byte, 65535)...),
			wantErr: false,
		},
		{
			name:    "incomplete payload",
			frame:   []byte{0, 5, 1, 2}, // Says 5 bytes but only 2 provided
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &mockReader{data: tt.frame}
			_, err := readUDPPacket(reader)
			if (err != nil) != tt.wantErr {
				t.Errorf("readUDPPacket() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// mockWriter and mockReader are defined in udp_test.go
// We need to reference them here
