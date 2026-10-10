package transport

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"customwireguard/internal/config"
	"customwireguard/internal/wire"
)

func fecNative(index, size int) []byte {
	p := make([]byte, size)
	for i := range p {
		p[i] = byte(i + index)
	}
	binary.LittleEndian.PutUint32(p, 4)
	binary.LittleEndian.PutUint32(p[4:], uint32(index))
	return p
}

func fecFixture(t *testing.T) ([][]byte, [][]byte, [][]byte) {
	t.Helper()
	now := time.Unix(1000, 0)
	s := fecSender{stats: new(Stats)}
	var native, encoded, parity [][]byte
	for i := 0; i < 16; i++ {
		p := fecNative(i, 32+i*79)
		native = append(native, p)
		encoded = append(encoded, s.push(p, now))
	}
	for s.count > 0 {
		parity = append(parity, s.popParity(now))
	}
	if len(parity) != 4 {
		t.Fatal("interleaved block did not produce four parity packets")
	}
	for _, p := range append(encoded, parity...) {
		if len(p) > wire.ExchangeMaxPayload {
			t.Fatal("FEC exceeded exchange budget")
		}
	}
	return native, encoded, parity
}

func TestFECRecoversEveryIsolatedLossAndFourPacketBurst(t *testing.T) {
	native, encoded, parity := fecFixture(t)
	for start := 0; start < 16; start++ {
		for _, width := range []int{1, 4} {
			if start+width > 16 {
				continue
			}
			for _, parityFirst := range []bool{false, true} {
				s := new(Stats)
				got := map[uint32][]byte{}
				r := fecReceiver{stats: s, emit: func(p []byte) {
					id := binary.LittleEndian.Uint32(p[4:])
					if _, ok := got[id]; ok {
						t.Fatal("duplicate recovery")
					}
					got[id] = p
				}}
				feed := func(packets [][]byte) {
					for _, p := range packets {
						r.push(p, time.Unix(1000, 0))
					}
				}
				if parityFirst {
					feed(parity)
				}
				// Reverse arrival order proves reconstruction does not depend on order.
				for i := len(encoded) - 1; i >= 0; i-- {
					if i < start || i >= start+width {
						r.push(encoded[i], time.Unix(1000, 0))
					}
				}
				if !parityFirst {
					feed(parity)
				}
				feed(parity) // fresh outer replay of parity must not reconstruct twice
				// Early parity may also reconstruct packets whose originals are
				// merely reordered. Their later arrival must not deliver twice.
				if len(got) != 16 || s.ExchangeFECRecovered.Load() < uint64(width) || s.ExchangeFECRecovered.Load() > 4 {
					t.Fatalf("loss start=%d width=%d first=%t got=%d stats=%v", start, width, parityFirst, len(got), s.Snapshot())
				}
				for i, p := range native {
					if !bytes.Equal(got[uint32(i)], p) {
						t.Fatal("reconstructed bytes changed")
					}
				}
			}
		}
	}
}

func TestFECDoesNotGuessTwoLossesInOneGroup(t *testing.T) {
	_, encoded, parity := fecFixture(t)
	count := 0
	s := new(Stats)
	r := fecReceiver{stats: s, emit: func([]byte) { count++ }}
	for i, p := range encoded {
		if i != 0 && i != 4 {
			r.push(p, time.Now())
		}
	}
	for _, p := range parity {
		r.push(p, time.Now())
	}
	if count != 14 || s.ExchangeFECRecovered.Load() != 0 {
		t.Fatal("invented recovery from insufficient information")
	}
}

func TestFECOriginalsAreImmediateAndPartialGroupsFlush(t *testing.T) {
	now := time.Unix(1000, 0)
	s := fecSender{stats: new(Stats)}
	first := fecNative(7, 1120)
	p := s.push(first, now)
	var got [][]byte
	r := fecReceiver{stats: new(Stats), emit: func(p []byte) { got = append(got, p) }}
	r.push(p, now)
	if len(got) != 1 || !bytes.Equal(got[0], first) {
		t.Fatal("original waited for group")
	}
	if s.popParity(now.Add(7*time.Millisecond)) != nil {
		t.Fatal("partial group flushed too soon")
	}
	backup := s.popParity(now.Add(8 * time.Millisecond))
	if backup == nil {
		t.Fatal("partial group never flushed")
	}
	lost := fecReceiver{stats: new(Stats), emit: func(p []byte) {
		if !bytes.Equal(p, first) {
			t.Fatal("partial recovery changed bytes")
		}
	}}
	lost.push(backup, now.Add(8*time.Millisecond))
	if lost.stats.ExchangeFECRecovered.Load() != 1 {
		t.Fatal("isolated low-rate data not recovered")
	}
}

func TestFECBoundedQueuesExpiryResetAndMTUFallback(t *testing.T) {
	now := time.Unix(1000, 0)
	s := fecSender{stats: new(Stats)}
	for i := 0; i < 200; i++ {
		s.push(fecNative(i, 1280), now)
	}
	if s.count != 16 || s.stats.ExchangeFECExpired.Load() != 32 {
		t.Fatal("unbounded parity cache", s.count, s.stats.Snapshot())
	}
	if s.popParity(now.Add(50*time.Millisecond)) != nil || s.count != 0 {
		t.Fatal("stale parity emitted")
	}
	s.push(fecNative(1, 1120), now)
	s.reset()
	if s.count != 0 || s.nextID != 0 || s.popParity(now.Add(time.Second)) != nil {
		t.Fatal("association reset retained data")
	}
	for _, size := range []int{1281, 1312} {
		p := fecNative(size, size)
		if !bytes.Equal(s.push(p, now), p) {
			t.Fatal("legacy MTU changed")
		}
	}
	handshake := make([]byte, 148)
	handshake[0] = 1
	if !bytes.Equal(s.push(handshake, now), handshake) || s.nextID != 0 {
		t.Fatal("handshake was wrapped")
	}
	r := fecReceiver{stats: new(Stats), emit: func([]byte) {}}
	for id := uint64(1); id <= 1000; id++ {
		r.push(fecFrame(id, 0, 0, fecNative(0, 32)), now)
	}
	r.push(fecFrame(1, 0, 0, fecNative(0, 32)), now)
	if r.newest != 1000 || r.stats.ExchangeFECLate.Load() != 1 {
		t.Fatal("late group polluted recovery window")
	}
	r.reset()
	if r.newest != 0 || r.groups[1000%fecReceiveGroups].id != 0 {
		t.Fatal("receiver state survived association")
	}
}

func TestFECParityCannotBeStarvedByBulkOrSmallTraffic(t *testing.T) {
	now := time.Unix(1000, 0)
	s := fecSender{stats: new(Stats)}
	q := newExchangeQueue(config.Config{ExchangeQueueMS: 60, ExchangeBurstMS: 350, ExchangeQueuePackets: 4096}, s.stats)
	for i := 0; i < 16; i++ {
		s.push(fecNative(i, 1120), now)
	}
	q.push(fecNative(100, 1120), now)
	if p, parity := s.next(&q, now); len(p) == 0 || !parity || q.length() != 1 {
		t.Fatal("bulk starved ready parity")
	}
	for i := 0; i < 20; i++ {
		q.push(fecNative(i+200, 64), now)
	}
	for i := 0; i < 4; i++ {
		if _, parity := s.next(&q, now); parity {
			t.Fatal("small originals lost bounded priority")
		}
	}
	if _, parity := s.next(&q, now); !parity {
		t.Fatal("small traffic starved parity")
	}
}

func TestFECMalformedMetadataNeverReachesNativeBackend(t *testing.T) {
	_, encoded, parity := fecFixture(t)
	mutations := []func([]byte){
		func(p []byte) { p[12] = 2 }, func(p []byte) { p[13] = 5 }, func(p []byte) { p[14] = 0xff }, func(p []byte) { clear(p[4:12]) },
	}
	for _, base := range [][]byte{encoded[0], parity[0]} {
		for _, mutate := range mutations {
			p := append([]byte(nil), base...)
			mutate(p)
			r := fecReceiver{stats: new(Stats), emit: func([]byte) { t.Fatal("malformed frame emitted") }}
			r.push(p, time.Now())
			if r.stats.ExchangeFECInvalid.Load() != 1 {
				t.Fatal("malformed frame not rejected")
			}
		}
	}
	p := append([]byte(nil), parity[0]...)
	binary.BigEndian.PutUint16(p[fecHeader:], 1281)
	r := fecReceiver{stats: new(Stats), emit: func([]byte) { t.Fatal("invalid length reconstructed") }}
	r.push(p, time.Now())
	if r.stats.ExchangeFECInvalid.Load() != 1 {
		t.Fatal("bad parity length accepted")
	}
}

func TestFECDoesNotSpendSmallPacketDeadlineOnParity(t *testing.T) {
	now := time.Unix(1000, 0)
	s := fecSender{stats: new(Stats)}
	for i := 0; i < 16; i++ {
		s.push(fecNative(i, 1120), now)
	}
	s.originals = 4
	q := newExchangeQueue(config.Config{ExchangeQueueMS: 60, ExchangeBurstMS: 350, ExchangeQueuePackets: 4096}, s.stats)
	q.push(fecNative(99, 64), now)
	p, parity := s.next(&q, now.Add(40*time.Millisecond))
	if parity || len(p) == 0 || q.length() != 0 {
		t.Fatal("parity delayed a small original near its deadline")
	}
}
