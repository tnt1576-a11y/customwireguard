package transport

import (
	"bytes"
	"testing"
	"time"
)

func TestExchangeRepairOneSpacedCopyAndBoundedLifetime(t *testing.T) {
	now := time.Unix(1000, 0)
	p := make([]byte, 1120)
	p[0] = 4
	p[20] = 17
	s := new(Stats)
	r := exchangeRepair{enabled: true, stats: s}
	r.remember(p, now)
	if r.pop(now.Add(4*time.Millisecond)) != nil {
		t.Fatal("copy not spaced")
	}
	// Simulate loss of the original: its exact authenticated native datagram
	// is still available once, without waiting for an acknowledgment.
	if !bytes.Equal(r.pop(now.Add(5*time.Millisecond)), p) || r.pop(now.Add(6*time.Millisecond)) != nil {
		t.Fatal("copy lost, modified or repeated")
	}
	r.remember(p, now)
	if r.pop(now.Add(30*time.Millisecond)) != nil || s.ExchangeRepairExpired.Load() != 1 {
		t.Fatal("stale backup sent")
	}
	for i := 0; i < 300; i++ {
		r.remember(p, now)
	}
	if r.count != 256 || s.ExchangeRepairExpired.Load() != 45 {
		t.Fatal("cache unbounded")
	}
	r.reset()
	if r.pop(now.Add(6*time.Millisecond)) != nil {
		t.Fatal("old association data retained")
	}
}

func TestExchangeRepairExcludesHandshakeAndDisabledMode(t *testing.T) {
	s := new(Stats)
	now := time.Now()
	r := exchangeRepair{enabled: true, stats: s}
	for _, n := range []int{0, 4, 31, 32, 148, 1313} {
		p := make([]byte, n)
		if n > 0 {
			p[0] = 1
		}
		r.remember(p, now)
		if n > 0 {
			p[0] = 4
			p[n-1] = 1
		}
		if n < 32 || n > 1312 {
			r.remember(p, now)
		}
	}
	if r.count != 0 {
		t.Fatal("non-data or oversize copied")
	}
	p := make([]byte, 32)
	p[0] = 4
	r.enabled = false
	r.remember(p, now)
	if r.count != 0 || r.pop(now.Add(time.Second)) != nil {
		t.Fatal("disabled repair retained data")
	}
}
