// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package security

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockReadWriteCloser implements io.ReadWriteCloser for testing
type mockReadWriteCloser struct {
	readData  []byte
	readErr   error
	writeData []byte
	writeErr  error
	closeErr  error
	closed    bool
}

func (m *mockReadWriteCloser) Read(b []byte) (n int, err error) {
	if m.readErr != nil {
		return 0, m.readErr
	}
	if len(m.readData) == 0 {
		return 0, io.EOF
	}
	n = copy(b, m.readData)
	m.readData = m.readData[n:]
	return n, nil
}

func (m *mockReadWriteCloser) Write(b []byte) (n int, err error) {
	if m.writeErr != nil {
		return 0, m.writeErr
	}
	m.writeData = append(m.writeData, b...)
	return len(b), nil
}

func (m *mockReadWriteCloser) Close() error {
	m.closed = true
	return m.closeErr
}

func TestNewClientPSK(t *testing.T) {
	secret := []byte("test-secret")
	psk := NewClientPSK(secret)

	require.NotNil(t, psk, "NewClientPSK() returned nil")
	if psk.secret == nil {
		t.Error("NewClientPSK() secret is nil")
	}
	if !bytes.Equal(psk.secret, secret) {
		t.Error("NewClientPSK() secret does not match input")
	}
}

func TestClientPSK_Wrap(t *testing.T) {
	secret := []byte("test-secret")
	tunnelID := "tunnel-123"
	psk := NewClientPSK(secret)

	base := &mockReadWriteCloser{}
	wrapped := psk.Wrap(base, tunnelID)

	require.NotNil(t, wrapped, "ClientPSK.Wrap() returned nil")
	if wrapped == base {
		t.Error("ClientPSK.Wrap() should return a new wrapper, not the base")
	}

	// Verify it's a ClientAEAD
	aead, ok := wrapped.(*ClientAEAD)
	if !ok {
		t.Fatal("ClientPSK.Wrap() should return *ClientAEAD")
	}

	// Verify key derivation: sha256(secret||tunnelID)
	// The key is used internally, so we can't directly access it
	// But we can verify the wrapper works by testing round-trip
	if aead.base != base {
		t.Error("ClientAEAD.base should reference the original connection")
	}
	if aead.aead == nil {
		t.Error("ClientAEAD.aead should be initialized")
	}
}

func TestClientAEAD_Write(t *testing.T) {
	secret := []byte("test-secret")
	tunnelID := "tunnel-123"
	psk := NewClientPSK(secret)

	base := &mockReadWriteCloser{}
	wrapped := psk.Wrap(base, tunnelID).(*ClientAEAD)

	testData := []byte("hello, world")
	n, err := wrapped.Write(testData)
	require.NoError(t, err, "ClientAEAD.Write() error = %v")
	assert.Equal(t, len(testData), n, "ClientAEAD.Write() = %d, want %d")

	// Verify data was written to base (should be encrypted)
	if len(base.writeData) == 0 {
		t.Error("ClientAEAD.Write() did not write to base connection")
	}

	// Verify frame format: [len(4)|nonce(24)|ct]
	// Minimum size: 4 (length) + 24 (nonce) + some ciphertext
	if len(base.writeData) < 4+24 {
		t.Errorf("ClientAEAD.Write() wrote %d bytes, want at least %d", len(base.writeData), 4+24)
	}

	// Verify counter increments
	initialCtr := wrapped.encCtr
	_, _ = wrapped.Write([]byte("test"))
	if wrapped.encCtr != initialCtr+1 {
		t.Errorf("ClientAEAD.Write() counter = %d, want %d", wrapped.encCtr, initialCtr+1)
	}
}

func TestClientAEAD_Read(t *testing.T) {
	secret := []byte("test-secret")
	tunnelID := "tunnel-123"
	psk := NewClientPSK(secret)

	// Create a writer to encrypt data
	writerBase := &mockReadWriteCloser{}
	writer := psk.Wrap(writerBase, tunnelID).(*ClientAEAD)

	// Write some data to get encrypted output
	testData := []byte("hello, world")
	_, err := writer.Write(testData)
	require.NoError(t, err, "ClientAEAD.Write() error = %v")

	// Create a reader with the encrypted data
	readerBase := &mockReadWriteCloser{
		readData: writerBase.writeData,
	}
	reader := psk.Wrap(readerBase, tunnelID).(*ClientAEAD)

	// Read and decrypt
	buf := make([]byte, 1024)
	n, err := reader.Read(buf)
	require.NoError(t, err, "ClientAEAD.Read() error = %v")
	assert.Equal(t, len(testData), n, "ClientAEAD.Read() = %d, want %d")
	if !bytes.Equal(buf[:n], testData) {
		t.Errorf("ClientAEAD.Read() = %q, want %q", buf[:n], testData)
	}
}

func TestClientAEAD_Read_ShortBuffer(t *testing.T) {
	secret := []byte("test-secret")
	tunnelID := "tunnel-123"
	psk := NewClientPSK(secret)

	// Create encrypted data
	writerBase := &mockReadWriteCloser{}
	writer := psk.Wrap(writerBase, tunnelID).(*ClientAEAD)
	testData := []byte("hello, world")
	_, err := writer.Write(testData)
	require.NoError(t, err)

	// Read with a buffer smaller than the decrypted frame
	readerBase := &mockReadWriteCloser{
		readData: writerBase.writeData,
	}
	reader := psk.Wrap(readerBase, tunnelID).(*ClientAEAD)

	// The rest of the frame is kept for the following reads, not dropped.
	smallBuf := make([]byte, 5)
	n, err := reader.Read(smallBuf)
	require.NoError(t, err)
	rest, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Equal(t, string(testData), string(smallBuf[:n])+string(rest))
}

func TestClientPSK_Wrap_UsesRandomNoncePrefix(t *testing.T) {
	psk := NewClientPSK([]byte("test-secret"))
	first, second := &mockReadWriteCloser{}, &mockReadWriteCloser{}
	_, err := psk.Wrap(first, "tunnel-123").Write([]byte("same payload"))
	require.NoError(t, err)
	_, err = psk.Wrap(second, "tunnel-123").Write([]byte("same payload"))
	require.NoError(t, err)

	prefix := func(frame []byte) []byte { return frame[lengthFieldSize : lengthFieldSize+noncePrefixSize] }
	assert.NotEqual(t, prefix(first.writeData), prefix(second.writeData), "each connection needs its own nonce prefix")
	assert.NotEqual(t, make([]byte, noncePrefixSize), prefix(first.writeData))
	assert.NotEqual(t, first.writeData[frameHeaderSize:], second.writeData[frameHeaderSize:])
}

func TestClientAEAD_Read_LegacyZeroPrefixNonce(t *testing.T) {
	psk := NewClientPSK([]byte("test-secret"))
	legacy := psk.Wrap(&mockReadWriteCloser{}, "tunnel-123").(*ClientAEAD)
	legacy.noncePrefix = [noncePrefixSize]byte{}
	_, err := legacy.Write([]byte("legacy peer"))
	require.NoError(t, err)
	wire := legacy.base.(*mockReadWriteCloser).writeData

	got, err := io.ReadAll(psk.Wrap(&mockReadWriteCloser{readData: wire}, "tunnel-123"))
	require.NoError(t, err)
	assert.Equal(t, "legacy peer", string(got))
}

func TestClientAEAD_Read_RejectsFrameLengthOutsideLimits(t *testing.T) {
	for _, size := range []uint32{0, 15, maxAEADFrameBytes + 1, 0xFFFFFFFF} {
		hdr := make([]byte, frameHeaderSize)
		binary.BigEndian.PutUint32(hdr, size)
		reader := NewClientPSK([]byte("test-secret")).Wrap(&mockReadWriteCloser{readData: hdr}, "tunnel-123")
		_, err := reader.Read(make([]byte, 16))
		require.ErrorIs(t, err, errFrameLength, "declared length %d", size)
	}
}

func TestClientAEAD_Write_SplitsPayloadAboveFrameCap(t *testing.T) {
	psk := NewClientPSK([]byte("test-secret"))
	payload := bytes.Repeat([]byte{0x5a}, maxFramePlaintext+10)
	base := &mockReadWriteCloser{}
	n, err := psk.Wrap(base, "tunnel-123").Write(payload)
	require.NoError(t, err)
	require.Equal(t, len(payload), n)
	require.EqualValues(t, maxAEADFrameBytes, binary.BigEndian.Uint32(base.writeData[:lengthFieldSize]))

	got, err := io.ReadAll(psk.Wrap(&mockReadWriteCloser{readData: base.writeData}, "tunnel-123"))
	require.NoError(t, err)
	require.True(t, bytes.Equal(payload, got), "payload split across frames must round-trip")
}

func TestClientAEAD_Write_EmptyPayloadEmitsNoFrame(t *testing.T) {
	base := &mockReadWriteCloser{}
	n, err := NewClientPSK([]byte("test-secret")).Wrap(base, "tunnel-123").Write(nil)
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.Empty(t, base.writeData)
}

func TestClientAEAD_Read_InvalidFrame(t *testing.T) {
	secret := []byte("test-secret")
	tunnelID := "tunnel-123"
	psk := NewClientPSK(secret)

	// Create reader with invalid data (too short)
	readerBase := &mockReadWriteCloser{
		readData: []byte{0, 0, 0}, // Too short for header
	}
	reader := psk.Wrap(readerBase, tunnelID).(*ClientAEAD)

	buf := make([]byte, 1024)
	_, err := reader.Read(buf)
	require.Error(t, err, "ClientAEAD.Read() with invalid frame should return error")
}

func TestClientAEAD_Read_CorruptedData(t *testing.T) {
	secret := []byte("test-secret")
	tunnelID := "tunnel-123"
	psk := NewClientPSK(secret)

	// Create valid header but corrupted ciphertext
	readerBase := &mockReadWriteCloser{
		readData: make([]byte, 4+24+16), // Header + some corrupted data
	}
	// Set a valid length
	readerBase.readData[0] = 0
	readerBase.readData[1] = 0
	readerBase.readData[2] = 0
	readerBase.readData[3] = 16 // 16 bytes of ciphertext

	reader := psk.Wrap(readerBase, tunnelID).(*ClientAEAD)

	buf := make([]byte, 1024)
	_, err := reader.Read(buf)
	require.Error(t, err, "ClientAEAD.Read() with corrupted data should return error")
}

func TestClientAEAD_Close(t *testing.T) {
	secret := []byte("test-secret")
	tunnelID := "tunnel-123"
	psk := NewClientPSK(secret)

	base := &mockReadWriteCloser{}
	wrapped := psk.Wrap(base, tunnelID).(*ClientAEAD)

	err := wrapped.Close()
	if err != nil {
		t.Errorf("ClientAEAD.Close() error = %v", err)
	}
	if !base.closed {
		t.Error("ClientAEAD.Close() did not close base connection")
	}
}

func TestClientAEAD_RoundTrip(t *testing.T) {
	secret := []byte("test-secret")
	tunnelID := "tunnel-123"
	psk := NewClientPSK(secret)

	// Create a writer connection
	writerBase := &mockReadWriteCloser{
		readData: make([]byte, 0),
	}
	writer := psk.Wrap(writerBase, tunnelID).(*ClientAEAD)

	// Write test data
	testData := []byte("round trip test data")
	_, err := writer.Write(testData)
	require.NoError(t, err, "ClientAEAD.Write() error = %v")

	// Create reader with the written encrypted data
	readerBase := &mockReadWriteCloser{
		readData: writerBase.writeData,
	}
	reader := psk.Wrap(readerBase, tunnelID).(*ClientAEAD)

	// Read and verify decryption
	readBuf := make([]byte, 1024)
	n, err := reader.Read(readBuf)
	require.NoError(t, err, "ClientAEAD.Read() error = %v")
	if !bytes.Equal(readBuf[:n], testData) {
		t.Errorf("ClientAEAD round-trip: read %q, want %q", readBuf[:n], testData)
	}
}

func TestClientAEAD_MultipleWrites(t *testing.T) {
	secret := []byte("test-secret")
	tunnelID := "tunnel-123"
	psk := NewClientPSK(secret)

	base := &mockReadWriteCloser{}
	wrapped := psk.Wrap(base, tunnelID).(*ClientAEAD)

	// Write multiple times
	testData1 := []byte("first")
	testData2 := []byte("second")
	testData3 := []byte("third")

	_, _ = wrapped.Write(testData1)
	_, _ = wrapped.Write(testData2)
	_, _ = wrapped.Write(testData3)

	// Verify counter incremented
	if wrapped.encCtr != 3 {
		t.Errorf("ClientAEAD multiple writes: counter = %d, want 3", wrapped.encCtr)
	}

	// Verify data was written
	if len(base.writeData) == 0 {
		t.Error("ClientAEAD multiple writes: no data written")
	}
}
