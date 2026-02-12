// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package security

import (
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"io"

	"golang.org/x/crypto/chacha20poly1305"

	"github.com/fortunnels/client/internal/support"
)

// PSK-based client-side crypto wrapper selector
type ClientPSK struct{ secret []byte }

type ClientAEAD struct {
	base   io.ReadWriteCloser
	aead   cipher.AEAD
	encCtr uint64
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
	return &ClientAEAD{base: conn, aead: a}
}

func (c *ClientAEAD) Read(p []byte) (int, error) {
	// frame: [len(4)|nonce(24)|ct]
	hdr := make([]byte, 4+24)
	if _, err := io.ReadFull(c.base, hdr); err != nil {
		return 0, err
	}
	l := binary.BigEndian.Uint32(hdr[:4])
	nonce := hdr[4:]
	buf := make([]byte, int(l))
	if _, err := io.ReadFull(c.base, buf); err != nil {
		return 0, err
	}
	pt, err := c.aead.Open(nil, nonce, buf, nil)
	if err != nil {
		return 0, err
	}
	n := copy(p, pt)
	if n < len(pt) {
		return n, io.ErrShortBuffer
	}
	return n, nil
}

func (c *ClientAEAD) Write(p []byte) (int, error) {
	// XChaCha20-Poly1305 requires 24-byte nonce; put counter in last 8 bytes
	nonce := make([]byte, 24)
	binary.BigEndian.PutUint64(nonce[16:], c.encCtr)
	c.encCtr++
	ct := c.aead.Seal(nil, nonce, p, nil)
	// ToUint32Size already validates the size limit, no need for duplicate check
	l, err := support.ToUint32Size(len(ct))
	if err != nil {
		return 0, err
	}
	hdr := make([]byte, 4+24)
	binary.BigEndian.PutUint32(hdr[:4], l)
	copy(hdr[4:], nonce)
	if _, err := c.base.Write(hdr); err != nil {
		return 0, err
	}
	if _, err := c.base.Write(ct); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *ClientAEAD) Close() error { return c.base.Close() }
