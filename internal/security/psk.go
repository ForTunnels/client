// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package security

import (
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/chacha20poly1305"

	"github.com/fortunnels/client/internal/support"
)

// Each frame is [ciphertext length (4 bytes, big-endian) | nonce (24 bytes) | ciphertext+tag].
const (
	lengthFieldSize = 4
	frameHeaderSize = lengthFieldSize + chacha20poly1305.NonceSizeX
	noncePrefixSize = 16
	// maxAEADFrameBytes caps the ciphertext+tag length a peer may declare; it
	// matches the server's limit.
	maxAEADFrameBytes = 16 << 20
	maxFramePlaintext = maxAEADFrameBytes - chacha20poly1305.Overhead
)

var errFrameLength = errors.New("security: invalid encrypted frame length")

// PSK-based client-side crypto wrapper selector
type ClientPSK struct{ secret []byte }

type ClientAEAD struct {
	base        io.ReadWriteCloser
	aead        cipher.AEAD
	noncePrefix [noncePrefixSize]byte
	encCtr      uint64
	pending     []byte // decrypted plaintext not yet returned
}

func NewClientPSK(secret []byte) *ClientPSK {
	return &ClientPSK{secret: secret}
}

func (c *ClientPSK) Wrap(conn io.ReadWriteCloser, tunnelID string) io.ReadWriteCloser {
	// mirror server derivation: sha256(secret||tunnelID)
	h := sha256.New()
	h.Write(c.secret)
	h.Write([]byte(tunnelID))
	key := h.Sum(nil)
	a, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil
	}
	w := &ClientAEAD{base: conn, aead: a}
	if _, randErr := rand.Read(w.noncePrefix[:]); randErr != nil {
		return nil
	}
	return w
}

// Read returns decrypted plaintext; a frame larger than p is delivered across
// several calls.
func (c *ClientAEAD) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(c.pending) == 0 {
		pt, err := c.readFrame()
		if err != nil {
			return 0, err
		}
		c.pending = pt
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	if len(c.pending) == 0 {
		c.pending = nil
	}
	return n, nil
}

// readFrame validates the declared length before allocating the frame.
func (c *ClientAEAD) readFrame() ([]byte, error) {
	hdr := make([]byte, frameHeaderSize)
	if _, err := io.ReadFull(c.base, hdr); err != nil {
		return nil, err
	}
	l := binary.BigEndian.Uint32(hdr[:lengthFieldSize])
	if l < chacha20poly1305.Overhead || l > maxAEADFrameBytes {
		return nil, fmt.Errorf("%w: %d bytes", errFrameLength, l)
	}
	buf := make([]byte, int(l))
	if _, err := io.ReadFull(c.base, buf); err != nil {
		return nil, err
	}
	return c.aead.Open(nil, hdr[lengthFieldSize:], buf, nil)
}

// Write encrypts p as one or more frames, each within maxAEADFrameBytes.
func (c *ClientAEAD) Write(p []byte) (int, error) {
	written := 0
	for written < len(p) {
		chunk := p[written:min(len(p), written+maxFramePlaintext)]
		if err := c.writeFrame(chunk); err != nil {
			return written, err
		}
		written += len(chunk)
	}
	return written, nil
}

func (c *ClientAEAD) writeFrame(p []byte) error {
	// XChaCha20-Poly1305 requires a 24-byte nonce: a random 16-byte prefix
	// fixed per connection, then a monotonic 8-byte counter. Both directions
	// and every stream of a tunnel share one key, so the random prefix is what
	// keeps a (key, nonce) pair from repeating across connections.
	nonce := make([]byte, chacha20poly1305.NonceSizeX)
	copy(nonce, c.noncePrefix[:])
	binary.BigEndian.PutUint64(nonce[noncePrefixSize:], c.encCtr)
	c.encCtr++
	// ToUint32Size already validates the size limit, no need for duplicate check
	l, err := support.ToUint32Size(len(p) + chacha20poly1305.Overhead)
	if err != nil {
		return err
	}
	frame := make([]byte, frameHeaderSize, frameHeaderSize+len(p)+chacha20poly1305.Overhead)
	binary.BigEndian.PutUint32(frame[:lengthFieldSize], l)
	copy(frame[lengthFieldSize:], nonce)
	frame = c.aead.Seal(frame, nonce, p, nil)
	_, err = c.base.Write(frame)
	return err
}

func (c *ClientAEAD) Close() error { return c.base.Close() }
