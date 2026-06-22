package proto

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"golang.org/x/crypto/chacha20poly1305"
)

// Wire packet layout (mirrors the reference aion2-common/src/crypto.rs):
//
//	[keyId:4][sessionId:8][nonce:24][ciphertext+tag]
//
// The encrypted plaintext is [formatTag:1][body], where formatTag distinguishes
// a raw Msg (FormatMsg) from an ARQ Frame (FormatFrame). AEAD has no associated
// data — only the plaintext is authenticated, same as the reference.
const (
	KeyIDSize     = 4
	SessionIDSize = 8
	NonceSize     = chacha20poly1305.NonceSizeX // 24
	TagSize       = 16
	HeaderSize    = KeyIDSize + SessionIDSize

	FormatMsg   byte = 0x00
	FormatFrame byte = 0x01
)

// SessionID identifies one client instance sharing a key (random per run).
type SessionID [SessionIDSize]byte

// KeyID is a non-cryptographic 4-byte fold of the key for O(1) relay lookup.
type KeyID [KeyIDSize]byte

// Key wraps an XChaCha20-Poly1305 cipher plus its derived KeyID.
type Key struct {
	c     cipherAEAD
	ID    KeyID
	bytes [32]byte // raw key, for callers that need to persist/echo it
}

// cipherAEAD is the subset of cipher.AEAD we use (kept small for clarity).
type cipherAEAD interface {
	Seal(dst, nonce, plaintext, additionalData []byte) []byte
	Open(dst, nonce, ciphertext, additionalData []byte) ([]byte, error)
}

// NewKey builds a Key from a 32-byte secret.
func NewKey(raw [32]byte) (*Key, error) {
	c, err := chacha20poly1305.NewX(raw[:])
	if err != nil {
		return nil, err
	}
	return &Key{c: c, ID: computeKeyID(raw), bytes: raw}, nil
}

// KeyFromHex parses a 64-hex-char (32-byte) key.
func KeyFromHex(s string) (*Key, error) {
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("key must be hex: %w", err)
	}
	if len(b) != 32 {
		return nil, fmt.Errorf("key must be 32 bytes (64 hex chars), got %d", len(b))
	}
	var raw [32]byte
	copy(raw[:], b)
	return NewKey(raw)
}

// computeKeyID folds 32 bytes into 4 (XOR by position, then a mix multiply),
// matching the reference so the same key yields the same id on both ends.
func computeKeyID(raw [32]byte) KeyID {
	var id [4]byte
	for i, b := range raw {
		id[i%4] ^= b
	}
	v := uint32(id[0]) | uint32(id[1])<<8 | uint32(id[2])<<16 | uint32(id[3])<<24
	v *= 0x9E3779B9
	return KeyID{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)}
}

// Seal encrypts body (with the given format tag) into a wire packet.
func (k *Key) Seal(session SessionID, format byte, body []byte) ([]byte, error) {
	nonce := make([]byte, NonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	pt := make([]byte, 1+len(body))
	pt[0] = format
	copy(pt[1:], body)

	out := make([]byte, 0, HeaderSize+NonceSize+len(pt)+TagSize)
	out = append(out, k.ID[:]...)
	out = append(out, session[:]...)
	out = append(out, nonce...)
	out = k.c.Seal(out, nonce, pt, nil)
	return out, nil
}

var errShortPacket = errors.New("proto: packet too short")

// Open decrypts a wire packet, returning the format tag and body.
func (k *Key) Open(packet []byte) (format byte, body []byte, err error) {
	if len(packet) < HeaderSize+NonceSize+TagSize {
		return 0, nil, errShortPacket
	}
	nonce := packet[HeaderSize : HeaderSize+NonceSize]
	ct := packet[HeaderSize+NonceSize:]
	pt, err := k.c.Open(nil, nonce, ct, nil)
	if err != nil {
		return 0, nil, err
	}
	if len(pt) < 1 {
		return 0, nil, errShortPacket
	}
	return pt[0], pt[1:], nil
}

// PeekKeyID extracts the key id from a packet header (for relay key lookup).
func PeekKeyID(packet []byte) (KeyID, bool) {
	if len(packet) < KeyIDSize {
		return KeyID{}, false
	}
	var id KeyID
	copy(id[:], packet[:KeyIDSize])
	return id, true
}

// PeekSessionID extracts the session id from a packet header.
func PeekSessionID(packet []byte) (SessionID, bool) {
	if len(packet) < HeaderSize {
		return SessionID{}, false
	}
	var s SessionID
	copy(s[:], packet[KeyIDSize:HeaderSize])
	return s, true
}

// NewSessionID returns a random session id.
func NewSessionID() (SessionID, error) {
	var s SessionID
	_, err := rand.Read(s[:])
	return s, err
}

// Hex returns the raw 32-byte key as 64 lowercase hex chars.
func (k *Key) Hex() string { return hex.EncodeToString(k.bytes[:]) }

// GenerateKeyHex returns a fresh random 32-byte key as 64 hex chars (used when
// deploying the relay).
func GenerateKeyHex() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}
