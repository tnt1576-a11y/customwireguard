package wire

import (
	"bytes"
	"testing"
	"time"
)

func TestExchangeCodecIsolationAndIntegrity(t *testing.T) {
	key := bytes.Repeat([]byte{42}, 32)
	c, _ := NewExchangeCodec(key, nil, true)
	s, _ := NewExchangeCodec(key, nil, false)
	legacy, _ := NewCodec(key, nil, false)
	f := Frame{Kind: Exchange, ID: NewID(), Sequence: 1, Timestamp: time.Now().Unix(), Payload: bytes.Repeat([]byte{7}, ExchangeMaxPayload)}
	p, err := c.Seal(f, ExchangeMinSize)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Open(p, time.Now())
	if err != nil || !bytes.Equal(got.Payload, f.Payload) {
		t.Fatal("exchange roundtrip", err)
	}
	if _, err := legacy.Open(p, time.Now()); err == nil {
		t.Fatal("legacy accepted exchange ciphertext")
	}
	if _, err := c.Open(p, time.Now()); err == nil {
		t.Fatal("reflection accepted")
	}
	for i := range p {
		bad := bytes.Clone(p)
		bad[i] ^= 1
		if _, err := s.Open(bad, time.Now()); err == nil {
			t.Fatal("tampered byte accepted", i)
		}
	}
	for i := 0; i < len(p); i++ {
		if _, err := s.Open(p[:i], time.Now()); err == nil {
			t.Fatal("truncation accepted", i)
		}
	}
	oldClient, _ := NewCodec(key, nil, true)
	probe, _ := oldClient.Seal(Frame{Kind: Probe, ID: NewID(), Timestamp: time.Now().Unix()}, 256)
	if _, err := s.Open(probe, time.Now()); err == nil {
		t.Fatal("legacy preflight accepted")
	}
	if _, err := oldClient.Seal(f, ExchangeMinSize); err == nil {
		t.Fatal("v1 allowed exchange kind")
	}
	session, _ := NewExchangeCodec(key, bytes.Repeat([]byte{1}, 48), false)
	if _, err := session.Open(p, time.Now()); err == nil {
		t.Fatal("bootstrap packet accepted as session")
	}
}
