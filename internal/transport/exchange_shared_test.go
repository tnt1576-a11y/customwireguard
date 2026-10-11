package transport

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"customwireguard/internal/wire"
)

func TestSharedGrantAcknowledgmentAndBorrowing(t *testing.T) {
	now := time.Unix(1700000000, 0)
	s := newSharedScheduler(8000)
	a := s.add()
	v, units := s.observe(a, now, 1, 80, true)
	if units != 68 {
		t.Fatal("unused capacity not lent", units)
	}
	b := s.add()
	s.observe(b, now.Add(50*time.Millisecond), 1, 80, true)
	if a.target != 35 || a.reserved != 68 || b.target != 2 {
		t.Fatal("capacity reused before reduction acknowledgment")
	}
	s.observe(a, now.Add(100*time.Millisecond), v, 80, true)
	if b.target != 2 {
		t.Fatal("stale acknowledgment released a newer grant")
	}
	s.observe(a, now.Add(150*time.Millisecond), a.version, 80, true)
	if a.reserved != 35 || b.target != 35 {
		t.Fatal("acknowledged capacity not shared")
	}
	c := s.add()
	s.observe(c, now.Add(200*time.Millisecond), 1, 80, true)
	for step := 5; step < 15; step++ {
		for _, l := range []*rateLease{a, b, c} {
			s.observe(l, now.Add(time.Duration(step)*50*time.Millisecond), l.version, 80, true)
		}
		if a.reserved+b.reserved+c.reserved > 72 {
			t.Fatal("grants oversubscribed")
		}
	}
	for _, l := range []*rateLease{a, b, c} {
		if l.target != 24 {
			t.Fatal("unequal saturated grants", l.target)
		}
	}
	// Make two users idle, then acknowledge their reduced grants. The busy
	// user can borrow, without forcing the idle ones to disconnect.
	for step := 15; step < 25; step++ {
		at := now.Add(time.Duration(step) * 50 * time.Millisecond)
		s.observe(a, at, a.version, 80, true)
		s.observe(b, at, b.version, 2, true)
		s.observe(c, at, c.version, 2, true)
		if a.reserved+b.reserved+c.reserved > 72 {
			t.Fatal("idle reclaim oversubscribed")
		}
	}
	if a.target != 68 || b.target != 2 || c.target != 2 {
		t.Fatal("idle capacity not borrowed")
	}
	previous := a.version
	s.observe(a, now.Add(1250*time.Millisecond), previous, 2, false)
	if a.target != 68 {
		t.Fatal("reordered request replaced current demand")
	}
}

func TestSharedBudgetLeavesArrivalHeadroom(t *testing.T) {
	for _, pps := range []int{600, 601, 800, 1200, 8000, 12000} {
		capacity := sharedCapacity(pps)
		if capacity < 6 || capacity*100 > pps {
			t.Fatal("invalid shared reservation capacity", pps, capacity)
		}
		if pps >= 800 && capacity*100 > pps*9/10 {
			t.Fatal("arrival headroom was consumed by grants", pps, capacity)
		}
	}
}

func TestSharedLeaseExpiryAndGenerationBound(t *testing.T) {
	now := time.Unix(1700000000, 0)
	s := newSharedScheduler(8000)
	a, b := s.add(), s.add()
	s.observe(a, now, 1, 80, true)
	if a.version != 2 {
		t.Fatal("one allocation incremented the version more than once")
	}
	grant := clientGrant{version: a.version, units: a.target, at: now}
	if grant.limit(now.Add(1100*time.Millisecond)) != 200 {
		t.Fatal("client kept expired grant")
	}
	grant.accept(a.version, a.target, now)
	if grant.limit(now.Add(1900*time.Millisecond)) != 200 {
		t.Fatal("delayed response renewed an old reservation")
	}
	s.observe(b, now.Add(1900*time.Millisecond), 1, 80, true)
	if a.reserved != 68 || b.target != 2 {
		t.Fatal("server reclaimed before silent lease expiry")
	}
	s.observe(b, now.Add(2100*time.Millisecond), b.version, 80, true)
	if a.reserved != 2 || b.target != 68 {
		t.Fatal("expired reservation not released")
	}
}

func TestSharedMaximumPayloadAndStrictNegotiation(t *testing.T) {
	now := time.Now()
	key, token := bytes.Repeat([]byte{0x43}, 32), bytes.Repeat([]byte{0x82}, 32)
	id := wire.NewID()
	client, _ := newAssociationMode(key, id, token, true, now, true)
	server, _ := newAssociationMode(key, id, token, false, now, true)
	client.shared, server.shared = true, true
	payload := bytes.Repeat([]byte{0xa5}, wire.ExchangeMaxPayload)
	packet, err := client.sealSized(wire.Exchange, sharedRequest(payload, 7, 8000, 8000), wire.ExchangeMinSize, now)
	if err != nil {
		t.Fatal(err)
	}
	frame, ok, _ := server.open(packet, now)
	if !ok || !bytes.Equal(frame.Payload[4:], payload) {
		t.Fatal("maximum shared request changed payload")
	}
	reply := make([]byte, 12+len(payload))
	binary.BigEndian.PutUint64(reply, frame.Sequence)
	binary.BigEndian.PutUint16(reply[9:], 8)
	reply[11] = 27
	copy(reply[12:], payload)
	packet, err = server.sealSized(wire.ExchangeReply, reply, wire.ExchangeMinSize, now)
	if err != nil {
		t.Fatal(err)
	}
	frame, ok, _ = client.open(packet, now)
	if !ok || !bytes.Equal(frame.Payload[12:], payload) {
		t.Fatal("maximum shared reply changed payload")
	}
	legacy, _ := newAssociationMode(key, id, token, true, now, true)
	if _, ok, _ = legacy.open(packet, now); ok {
		t.Fatal("legacy parser accepted larger unnegotiated shared metadata")
	}
	bad := append([]byte(nil), reply...)
	bad[11] = 0
	packet, _ = server.sealSized(wire.ExchangeReply, bad, wire.ExchangeMinSize, now)
	if _, ok, _ = client.open(packet, now); ok {
		t.Fatal("invalid grant accepted")
	}
}
