package transport

import (
	"net/netip"
	"testing"
	"time"

	"customwireguard/internal/config"
)

func TestPacerSpacingBoundsExpiryAndRebind(t *testing.T) {
	s := new(Stats)
	p := newPacketPacer(config.Config{PaceKbps: 1000, PaceQueueMS: 30}, nil, s)
	defer p.reset()
	var sent []packet
	p.write = func(q packet) bool { sent = append(sent, q); return true }
	now := time.Now().Add(time.Hour) // Logical clock; no sleeps or real sockets.
	addr := netip.MustParseAddrPort("127.0.0.1:1000")
	for i := 0; i < 10; i++ {
		p.push(make([]byte, 1200), addr, now)
	}
	if len(sent) != 1 || len(p.queue) != 3 || s.PacingDropped.Load() != 6 {
		t.Fatalf("burst/queue bound: sent=%d queued=%d stats=%v", len(sent), len(p.queue), s.Snapshot())
	}
	p.drain(now.Add(9 * time.Millisecond))
	if len(sent) != 1 {
		t.Fatal("sent before rate budget")
	}
	newAddr := netip.MustParseAddrPort("127.0.0.1:1001")
	p.rebind(newAddr)
	p.drain(now.Add(20 * time.Millisecond))
	if len(sent) != 2 || sent[1].addr != newAddr {
		t.Fatal("rebind or single-packet drain failed")
	}
	p.drain(now.Add(20 * time.Millisecond))
	if len(sent) != 2 {
		t.Fatal("caught up with a burst")
	}
	p.drain(now.Add(31 * time.Millisecond))
	if len(sent) != 2 || len(p.queue) != 0 || s.PacingDropped.Load() != 8 || s.PacingQueueBytes.Load() != 0 {
		t.Fatal("stale queued packets forwarded or unaccounted")
	}
	if s.PacingWaitUS.Load() != 20000 || s.Sent.Load() != 2 {
		t.Fatal("incorrect pacing counters")
	}
	p.push(make([]byte, 1200), addr, now.Add(time.Second))
	p.push(make([]byte, 1200), addr, now.Add(time.Second))
	p.reset()
	if p.ready() != nil || s.PacingQueueBytes.Load() != 0 || s.PacingDropped.Load() != 9 {
		t.Fatal("session reset leaked queued packets")
	}
}

func TestPacerDisabledAndFailedWrites(t *testing.T) {
	s := new(Stats)
	p := newPacketPacer(config.Config{}, nil, s)
	defer p.reset()
	n := 0
	p.write = func(packet) bool { n++; return n%2 == 0 }
	for i := 0; i < 10; i++ {
		p.push([]byte{1}, zeroAddr, time.Now())
	}
	if n != 10 || s.Sent.Load() != 5 || s.PacingQueued.Load() != 0 || p.ready() != nil {
		t.Fatal("unpaced path changed")
	}
}

func TestPacerHardPacketBound(t *testing.T) {
	s := new(Stats)
	p := newPacketPacer(config.Config{PaceKbps: 100000, PaceQueueMS: 1000}, nil, s)
	defer p.reset()
	p.write = func(packet) bool { return true }
	now := time.Now().Add(time.Hour)
	for i := 0; i < 1000; i++ {
		p.push(make([]byte, 1440), zeroAddr, now)
	}
	if len(p.queue) != 256 || s.PacingDropped.Load() != 743 || s.PacingPeakBytes.Load() != 256*1440 {
		t.Fatal("queue memory bound failed")
	}
}
