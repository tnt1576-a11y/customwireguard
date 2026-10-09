package wire

import (
	"bytes"
	"crypto/rand"
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
