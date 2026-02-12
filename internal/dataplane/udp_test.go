// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"testing"
)

// mockWriter implements io.Writer for testing
type mockWriter struct {
	data     []byte
	writeErr error
}

func (m *mockWriter) Write(b []byte) (int, error) {
	if m.writeErr != nil {
		return 0, m.writeErr
	}
	m.data = append(m.data, b...)
	return len(b), nil
}

// mockReader implements io.Reader for testing
type mockReader struct {
	data    []byte
	readErr error
}

func (m *mockReader) Read(b []byte) (int, error) {
	if m.readErr != nil {
		return 0, m.readErr
	}
	if len(m.data) == 0 {
		return 0, io.EOF
	}
	n := copy(b, m.data)
	m.data = m.data[n:]
	return n, nil
}

func TestWriteUDPPacket(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
		wantErr bool
	}{
		{
			name:    "small packet",
			payload: []byte("hello"),
			wantErr: false,
		},
		{
			name:    "empty packet",
			payload: []byte{},
			wantErr: false,
		},
		{
			name:    "large packet",
			payload: make([]byte, 65535),
			wantErr: false,
		},
		{
			name:    "max size packet",
			payload: make([]byte, 65507), // Max UDP payload size
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer := &mockWriter{}
			err := writeUDPPacket(writer, tt.payload)
			if (err != nil) != tt.wantErr {
				t.Errorf("writeUDPPacket() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				// Verify frame format: [len(2)|payload]
				if len(writer.data) < 2 {
					t.Error("writeUDPPacket() should write at least 2 bytes (header)")
					return
				}
				// Verify length header
				length := binary.BigEndian.Uint16(writer.data[:2])
				if length != uint16(len(tt.payload)) {
					t.Errorf("writeUDPPacket() length header = %d, want %d", length, len(tt.payload))
				}
				// Verify payload
				if len(writer.data) != 2+len(tt.payload) {
					t.Errorf("writeUDPPacket() total length = %d, want %d", len(writer.data), 2+len(tt.payload))
				}
				if !bytes.Equal(writer.data[2:], tt.payload) {
					t.Error("writeUDPPacket() payload does not match")
				}
			}
		})
	}
}

func TestWriteUDPPacket_WriteError(t *testing.T) {
	writer := &mockWriter{
		writeErr: io.ErrClosedPipe,
	}
	payload := []byte("test")
	err := writeUDPPacket(writer, payload)
	if err == nil {
		t.Error("writeUDPPacket() with write error should return error")
	}
}

func TestReadUDPPacket(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
		wantErr bool
	}{
		{
			name:    "small packet",
			payload: []byte("hello"),
			wantErr: false,
		},
		{
			name:    "empty packet",
			payload: []byte{},
			wantErr: true, // readUDPPacket returns error for n <= 0
		},
		{
			name:    "large packet",
			payload: make([]byte, 1000),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create frame: [len(2)|payload]
			frame := make([]byte, 2+len(tt.payload))
			binary.BigEndian.PutUint16(frame[:2], uint16(len(tt.payload)))
			copy(frame[2:], tt.payload)

			reader := &mockReader{data: frame}
			result, err := readUDPPacket(reader)
			if (err != nil) != tt.wantErr {
				t.Errorf("readUDPPacket() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if !bytes.Equal(result, tt.payload) {
					t.Errorf("readUDPPacket() = %q, want %q", result, tt.payload)
				}
			}
		})
	}
}

func TestReadUDPPacket_InvalidSize(t *testing.T) {
	tests := []struct {
		name    string
		frame   []byte
		wantErr bool
	}{
		{
			name:    "zero length",
			frame:   []byte{0, 0},
			wantErr: true,
		},
		{
			name:    "incomplete header",
			frame:   []byte{0},
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

func TestReadUDPPacket_EOF(t *testing.T) {
	reader := &mockReader{
		readErr: io.EOF,
	}
	_, err := readUDPPacket(reader)
	if err == nil {
		t.Error("readUDPPacket() with EOF should return error")
	}
}

func TestSendUDPPreface(t *testing.T) {
	tests := []struct {
		name     string
		dst      string
		tunnelID string
		wantErr  bool
	}{
		{
			name:     "valid preface",
			dst:      "127.0.0.1:8080",
			tunnelID: "tunnel-123",
			wantErr:  false,
		},
		{
			name:     "empty dst",
			dst:      "",
			tunnelID: "tunnel-123",
			wantErr:  false,
		},
		{
			name:     "empty tunnelID",
			dst:      "127.0.0.1:8080",
			tunnelID: "",
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
				// Verify JSON preface was written
				if len(writer.data) == 0 {
					t.Error("sendUDPPreface() should write data")
					return
				}
				// Verify it ends with newline
				if writer.data[len(writer.data)-1] != '\n' {
					t.Error("sendUDPPreface() should end with newline")
				}
				// Verify it's valid JSON
				var preface map[string]string
				jsonData := writer.data[:len(writer.data)-1] // Remove newline
				if err := json.Unmarshal(jsonData, &preface); err != nil {
					t.Errorf("sendUDPPreface() wrote invalid JSON: %v", err)
				}
				// Verify fields
				if preface["dst"] != tt.dst {
					t.Errorf("sendUDPPreface() dst = %q, want %q", preface["dst"], tt.dst)
				}
				if preface["proto"] != "udp" {
					t.Errorf("sendUDPPreface() proto = %q, want %q", preface["proto"], "udp")
				}
				if preface["tunnel_id"] != tt.tunnelID {
					t.Errorf("sendUDPPreface() tunnel_id = %q, want %q", preface["tunnel_id"], tt.tunnelID)
				}
			}
		})
	}
}

func TestSendUDPPreface_WriteError(t *testing.T) {
	writer := &mockWriter{
		writeErr: io.ErrClosedPipe,
	}
	err := sendUDPPreface(writer, "127.0.0.1:8080", "tunnel-123")
	if err == nil {
		t.Error("sendUDPPreface() with write error should return error")
	}
}
