package wire

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"math"
	"testing"
	"time"
)

func pair(t testing.TB, material []byte) (*Codec, *Codec) {
	t.Helper()
	key := make([]byte, 32)
	rand.Read(key)
	c, e := NewCodec(key, material, true)
	if e != nil {
		t.Fatal(e)
	}
	s, e := NewCodec(key, material, false)
	if e != nil {
		t.Fatal(e)
	}
	return c, s
}

func TestScratchCodecCompatibilityAndOwnership(t *testing.T) {
	for _, version := range []byte{1, 2} {
		c, _ := newCodec(bytes.Repeat([]byte{9}, 32), nil, true, version)
		s, _ := newCodec(bytes.Repeat([]byte{9}, 32), nil, false, version)
		now := time.Unix(2000000000, 0)
		dst := bytes.Repeat([]byte{0xff}, MaxDatagram)
		for _, length := range []int{1312, 0, 32, 1280} {
			f := Frame{Kind: Data, ID: NewID(), Sequence: uint64(length + 1), Timestamp: now.Unix(), Payload: bytes.Repeat([]byte{7}, length)}
			packet, err := c.SealInto(dst, f, 1400)
			if err != nil || len(packet) != 1400 || &packet[0] != &dst[0] {
				t.Fatalf("scratch destination: %v", err)
			}
			// Decode directly using the previous on-wire header layout, independently
			// of Codec.Open. No version, nonce size, header or padding rule changed.
			plain, err := s.rx.Open(nil, packet[:24], packet[24:], nil)
			if err != nil || plain[0] != version || plain[1] != f.Kind || binary.BigEndian.Uint64(plain[2:10]) != uint64(f.Timestamp) || !bytes.Equal(plain[10:26], f.ID[:]) || binary.BigEndian.Uint64(plain[26:34]) != f.Sequence || int(binary.BigEndian.Uint16(plain[34:36])) != length || !bytes.Equal(plain[36:36+length], f.Payload) {
				t.Fatalf("legacy layout differs: %v", err)
			}
			original := bytes.Clone(packet)
			ordinary, err := s.Open(packet, now)
			if err != nil || !bytes.Equal(packet, original) {
				t.Fatal("ordinary Open must not consume shared input")
			}
			owned := bytes.Clone(packet)
			opened, err := s.OpenInPlace(owned, now)
			if err != nil || opened.ID != ordinary.ID || opened.Sequence != ordinary.Sequence || !bytes.Equal(opened.Payload, f.Payload) {
				t.Fatalf("in-place decode: %v", err)
			}
			if length > 0 && &opened.Payload[0] != &owned[24+HeaderSize] {
				t.Fatal("OpenInPlace allocated a second payload buffer")
			}
			if _, err = c.SealInto(dst, f, 1440); err != nil || !bytes.Equal(opened.Payload, f.Payload) {
				t.Fatal("next send overwrote separately owned receive payload")
			}
			for i := range original {
				bad := bytes.Clone(original)
				bad[i] ^= 1
				if got, err := s.OpenInPlace(bad, now); err == nil || got.Payload != nil {
					t.Fatalf("in-place tampering at %d accepted", i)
				}
			}
		}
	}
}

func BenchmarkSealOpenScratch1280(b *testing.B) {
	c, s := pair(b, bytes.Repeat([]byte{8}, 48))
	now := time.Now()
	f := Frame{Kind: Data, ID: NewID(), Sequence: 1, Timestamp: now.Unix(), Payload: make([]byte, 1312)}
	dst := make([]byte, MaxDatagram)
	b.SetBytes(1280)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		p, e := c.SealInto(dst, f, 1400)
		if e != nil {
			b.Fatal(e)
		}
		if _, e := s.OpenInPlace(p, now); e != nil {
			b.Fatal(e)
		}
	}
}

func TestFrameIntegrityAndDirections(t *testing.T) {
	c, s := pair(t, nil)
	now := time.Now()
	f := Frame{Kind: Data, ID: NewID(), Sequence: 42, Timestamp: now.Unix(), Payload: []byte("WireGuard encrypted payload")}
	p, e := c.Seal(f, 256)
	if e != nil {
		t.Fatal(e)
	}
	got, e := s.Open(p, now)
	if e != nil || !bytes.Equal(got.Payload, f.Payload) || got.ID != f.ID || got.Sequence != 42 {
		t.Fatalf("roundtrip: %v", e)
	}
	if _, e = c.Open(p, now); e == nil {
		t.Fatal("reflection accepted")
	}
	for i := range p {
		bad := bytes.Clone(p)
		bad[i] ^= 1
		if _, e = s.Open(bad, now); e == nil {
			t.Fatalf("tamper at byte %d accepted", i)
		}
	}
	for i := 0; i < len(p); i++ {
		if _, e = s.Open(p[:i], now); e == nil {
			t.Fatalf("truncation %d accepted", i)
		}
	}
	p2, _ := c.Seal(f, 256)
	if bytes.Equal(p[:24], p2[:24]) {
		t.Fatal("nonce reused")
	}
	if bytes.Contains(p, f.Payload) {
		t.Fatal("plaintext visible")
	}
	other, _ := pair(t, nil)
	if _, e = other.Open(p, now); e == nil {
		t.Fatal("wrong key accepted")
	}
}

func TestTimestampBounds(t *testing.T) {
	c, s := pair(t, nil)
	now := time.Unix(2000000000, 0)
	for _, ts := range []int64{now.Unix() - 121, now.Unix() + 31, math.MinInt64, math.MaxInt64} {
		p, _ := c.Seal(Frame{Kind: Hello, ID: NewID(), Timestamp: ts}, 256)
		if _, e := s.Open(p, now); e == nil {
			t.Fatalf("invalid time %d accepted", ts)
		}
	}
}

func TestSessionKeysChange(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)
	material := make([]byte, 48)
	rand.Read(material)
	c, _ := NewCodec(key, material, true)
	s, _ := NewCodec(key, material, false)
	root, _ := NewCodec(key, nil, false)
	p, _ := c.Seal(Frame{Kind: Data, ID: NewID(), Sequence: 1, Timestamp: time.Now().Unix(), Payload: []byte{1}}, 128)
	if _, e := s.Open(p, time.Now()); e != nil {
		t.Fatal(e)
	}
	if _, e := root.Open(p, time.Now()); e == nil {
		t.Fatal("session packet accepted under bootstrap key")
	}
	material[20] ^= 1
	fresh, _ := NewCodec(key, material, false)
	if _, e := fresh.Open(p, time.Now()); e == nil {
		t.Fatal("old packet accepted after new server challenge")
	}
}

func TestPaddingBudget(t *testing.T) {
	for _, profile := range []string{"minimal", "balanced"} {
		for payload := 0; payload <= MaxPayload; payload++ {
			n, e := PacketSize(payload, profile)
			if e != nil || n < Overhead+payload || n > MaxDatagram {
				t.Fatalf("budget: %s %d %d %v", profile, payload, n, e)
			}
		}
	}
	if _, e := PacketSize(MaxPayload+1, "balanced"); e == nil {
		t.Fatal("oversize accepted")
	}
}

func TestReplayWindow(t *testing.T) {
	var w ReplayWindow
	for _, seq := range []uint64{3, 1, 2, 2049, 2048, math.MaxUint64 - 1, math.MaxUint64} {
		if ok, _ := w.Accept(seq); !ok {
			t.Fatalf("fresh sequence %d rejected", seq)
		}
		if ok, _ := w.Accept(seq); ok {
			t.Fatalf("replay %d accepted", seq)
		}
	}
	if ok, _ := w.Accept(0); ok {
		t.Fatal("zero accepted")
	}
	if ok, _ := w.Accept(1); ok {
		t.Fatal("old sequence accepted")
	}
}

func FuzzOpen(f *testing.F) {
	key := bytes.Repeat([]byte{7}, 32)
	c, _ := NewCodec(key, nil, true)
	s, _ := NewCodec(key, nil, false)
	p, _ := c.Seal(Frame{Kind: Data, Timestamp: 2000000000, ID: NewID(), Sequence: 1, Payload: []byte{1, 2, 3}}, 256)
	f.Add(p)
	f.Add([]byte{})
	f.Add(bytes.Repeat([]byte{0}, 1440))
	f.Fuzz(func(t *testing.T, p []byte) {
		got, e := s.Open(p, time.Unix(2000000000, 0))
		consumed, consumeErr := s.OpenInPlace(bytes.Clone(p), time.Unix(2000000000, 0))
		if (e == nil) != (consumeErr == nil) || (e == nil &&
			(got.Kind != consumed.Kind || got.ID != consumed.ID || got.Sequence != consumed.Sequence ||
				got.Timestamp != consumed.Timestamp || !bytes.Equal(got.Payload, consumed.Payload))) {
			t.Fatal("in-place decode differs from ordinary decode")
		}
		if e == nil && len(got.Payload) > MaxPayload {
			t.Fatal("unbounded payload")
		}
	})
}

func BenchmarkSealOpen1280(b *testing.B) {
	c, s := pair(b, bytes.Repeat([]byte{8}, 48))
	now := time.Now()
	f := Frame{Kind: Data, ID: NewID(), Sequence: 1, Timestamp: now.Unix(), Payload: make([]byte, 1312)}
	b.SetBytes(1280)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		p, e := c.Seal(f, 1400)
		if e != nil {
			b.Fatal(e)
		}
		if _, e = s.Open(p, now); e != nil {
			b.Fatal(e)
		}
	}
}
