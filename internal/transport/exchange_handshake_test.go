package transport

import (
	"bytes"
	"testing"
	"time"

	"customwireguard/internal/config"
)

func TestNativeHandshakeQueueSurvivesIdleGapWithoutExtendingData(t *testing.T) {
	for _, cfg := range []config.Config{{}, {ExchangeQueueMS: 60, ExchangeBurstMS: 350, ExchangeQueuePackets: 4096}} {
		now := time.Unix(1000, 0)
		stats := new(Stats)
		q := newExchangeQueue(cfg, stats)
		q.push(fecNative(1, 64), now)
		q.push(fecNative(2, 1120), now)
		handshake := make([]byte, 92)
		handshake[0] = 2
		q.push(handshake, now)
		sender := fecSender{stats: stats}
		for i := 0; i < 16; i++ {
			sender.push(fecNative(i, 1120), now)
		}
		// A handshake reply previously expired during a short request gap,
		// forcing the native device to wait about five seconds for a retry.
		p, parity := sender.next(&q, now.Add(100*time.Millisecond))
		if parity || !bytes.Equal(p, handshake) || stats.ExchangeControlSent.Load() != 1 {
			t.Fatal("handshake expired or waited behind data/parity", stats.Snapshot())
		}
		q.expire(now.Add(501 * time.Millisecond))
		if q.length() != 0 || stats.ExchangeQueueExpired.Load() != 2 || stats.ExchangeControlExpired.Load() != 0 {
			t.Fatal("data deadlines changed", stats.Snapshot())
		}
	}
}

func TestNativeHandshakeQueueIsBoundedAndResets(t *testing.T) {
	now := time.Unix(1000, 0)
	stats := new(Stats)
	q := newExchangeQueue(config.Config{ExchangeQueueMS: 60, ExchangeBurstMS: 350}, stats)
	for kind, size := range map[byte]int{1: 148, 2: 92, 3: 64} {
		p := make([]byte, size)
		p[0] = kind
		if !nativeHandshake(p) || nativeHandshake(p[:size-1]) {
			t.Fatal("incorrect native handshake shape")
		}
		p[1] = 1
		if nativeHandshake(p) {
			t.Fatal("nonzero reserved bytes accepted")
		}
	}
	p := make([]byte, 148)
	p[0] = 1
	for i := 0; i < 9; i++ {
		q.push(p, now)
	}
	if q.length() != 8 || stats.ExchangeControlQueueFull.Load() != 1 || stats.ExchangeQueueFull.Load() != 1 {
		t.Fatal("control queue exceeded fixed bound", stats.Snapshot())
	}
	q.expire(now.Add(time.Second + time.Nanosecond))
	if q.length() != 0 || stats.ExchangeControlExpired.Load() != 8 {
		t.Fatal("control queue retained stale handshake", stats.Snapshot())
	}
	q.push(p, now)
	q.rebase(now.Add(time.Second))
	if q.pop(now.Add(time.Second)) == nil {
		t.Fatal("association startup did not rebase control queue")
	}
	q.push(p, now)
	q.clear()
	if len(q.control) != 0 || q.length() != 0 {
		t.Fatal("association reset retained control data")
	}
}
