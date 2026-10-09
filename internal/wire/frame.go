// Package wire implements the experimental CWG v1 outer transport. It does not
// change WireGuard cryptography. No CWG version, type or counter is sent in clear.
package wire

import (
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
)

const (
	Hello       byte = 1
	Welcome     byte = 2
	Data        byte = 3
	Ping        byte = 4
	Pong        byte = 5
	Probe       byte = 6
	ProbeReply  byte = 7
	HeaderSize       = 36
	Overhead         = chacha20poly1305.NonceSizeX + chacha20poly1305.Overhead + HeaderSize
	MaxDatagram      = 1440
	MaxPayload       = MaxDatagram - Overhead
)

var ErrPacket = errors.New("invalid or unauthenticated datagram")

type ID [16]byte

func NewID() ID {
	var id ID
	rand.Read(id[:])
	return id
}

type Frame struct {
	Kind      byte
	ID        ID
	Sequence  uint64
	Timestamp int64
	Payload   []byte
}

type Codec struct {
	tx cipher.AEAD
	rx cipher.AEAD
}

// NewCodec derives independent keys for directions and for bootstrap/session use.
// Session material is a fresh 16-byte client ID followed by a 32-byte server token.
func NewCodec(key []byte, material []byte, client bool) (*Codec, error) {
	if len(key) != 32 || (len(material) != 0 && len(material) != 48) {
		return nil, errors.New("expected a 32-byte key and either no material or 48-byte session material")
	}
	domain := "customwireguard/v1/bootstrap/"
	if len(material) != 0 {
		domain = "customwireguard/v1/session/"
	}
	makeAEAD := func(direction string) (cipher.AEAD, error) {
		derived, err := hkdf.Key(sha256.New, key, material, domain+direction, 32)
		if err != nil {
			return nil, err
		}
		return chacha20poly1305.NewX(derived)
	}
	c2s, err := makeAEAD("client-to-server")
	if err != nil {
		return nil, err
	}
	s2c, err := makeAEAD("server-to-client")
	if err != nil {
		return nil, err
	}
	if client {
		return &Codec{tx: c2s, rx: s2c}, nil
	}
	return &Codec{tx: s2c, rx: c2s}, nil
}

func (c *Codec) Seal(f Frame, size int) ([]byte, error) {
	if size < Overhead+len(f.Payload) || size > MaxDatagram || f.Kind < Hello || f.Kind > ProbeReply {
		return nil, fmt.Errorf("frame exceeds datagram budget or has invalid type")
	}
	nonce := make([]byte, chacha20poly1305.NonceSizeX, size)
	rand.Read(nonce)
	plain := make([]byte, size-chacha20poly1305.NonceSizeX-chacha20poly1305.Overhead)
	plain[0], plain[1] = 1, f.Kind
	binary.BigEndian.PutUint64(plain[2:10], uint64(f.Timestamp))
	copy(plain[10:26], f.ID[:])
	binary.BigEndian.PutUint64(plain[26:34], f.Sequence)
	binary.BigEndian.PutUint16(plain[34:36], uint16(len(f.Payload)))
	copy(plain[36:], f.Payload)
	rand.Read(plain[36+len(f.Payload):])
	return c.tx.Seal(nonce, nonce, plain, nil), nil
}

func (c *Codec) Open(packet []byte, now time.Time) (Frame, error) {
	var f Frame
	if len(packet) < Overhead || len(packet) > MaxDatagram {
		return f, ErrPacket
	}
	n := chacha20poly1305.NonceSizeX
	plain, err := c.rx.Open(nil, packet[:n], packet[n:], nil)
	if err != nil || len(plain) < HeaderSize {
		return f, ErrPacket
	}
	if plain[0] != 1 || plain[1] < Hello || plain[1] > ProbeReply {
		return f, ErrPacket
	}
	ts := int64(binary.BigEndian.Uint64(plain[2:10]))
	// Compare bounds instead of subtracting attacker-controlled signed integers.
	if ts < now.Unix()-120 || ts > now.Unix()+30 {
		return f, ErrPacket
	}
	length := int(binary.BigEndian.Uint16(plain[34:36]))
	if length > len(plain)-HeaderSize {
		return f, ErrPacket
	}
	f.Kind, f.Timestamp = plain[1], ts
	copy(f.ID[:], plain[10:26])
	f.Sequence = binary.BigEndian.Uint64(plain[26:34])
	f.Payload = plain[36 : 36+length]
	return f, nil
}

// PacketSize chooses lengths independently per packet; no sleeps or batching are
// introduced. Small packets include WireGuard handshakes and empty keepalives.
func PacketSize(payload int, profile string) (int, error) {
	base := Overhead + payload
	if payload < 0 || base > MaxDatagram {
		return 0, errors.New("payload too large; lower the WireGuard MTU")
	}
	low, high := base, base+15
	if profile == "balanced" {
		high = base + 64
		if payload <= 192 {
			low, high = 256, 512
			if low < base {
				low = base
			}
		}
	}
	if high > MaxDatagram {
		high = MaxDatagram
	}
	return RandomBetween(low, high), nil
}

func RandomBetween(low, high int) int {
	if high <= low {
		return low
	}
	// Rejection sampling avoids modulo bias for configurable ranges.
	span := uint64(high - low + 1)
	limit := ^uint64(0) - (^uint64(0) % span)
	var b [8]byte
	for {
		rand.Read(b[:])
		n := binary.LittleEndian.Uint64(b[:])
		if n < limit {
			return low + int(n%span)
		}
	}
}
