package transport

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

func TestBurstIngressBoundedAndAttributed(t *testing.T) {
	public, backend, source := socket(t), socket(t), socket(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stats := new(Stats)
	channels, cleanup := readersSized(ctx, stats, []int{256, 2048}, public, backend)
	defer cleanup()
	started := time.Now()
	// Leave both event-loop handoffs unconsumed. Wait for each read so kernel
	// buffering and host scheduling cannot cause an unrelated test failure.
	for index, count := range []int{280, 2080} {
		target := []*net.UDPConn{public, backend}[index].LocalAddr().(*net.UDPAddr)
		for n := 1; n <= count; n++ {
			payload := make([]byte, 1100)
			binary.BigEndian.PutUint32(payload, uint32(n))
			if _, err := source.WriteToUDP(payload, target); err != nil {
				t.Fatal(err)
			}
			until := time.Now().Add(time.Second)
			for len(channels[index])+int(stats.ReaderDropped[index].Load()) < n {
				if time.Now().After(until) {
					t.Fatal("socket reader did not account for packet", index, n)
				}
				time.Sleep(time.Millisecond)
			}
		}
	}
	if len(channels[0]) != 256 || len(channels[1]) != 2048 || stats.QueueDropped.Load() != 56 {
		t.Fatal("input handoff not bounded", stats.Snapshot())
	}
	if stats.ReaderDropped[0].Load() != 24 || stats.ReaderDropped[1].Load() != 32 {
		t.Fatal("drop source misattributed", stats.Snapshot())
	}
	if stats.ReaderPeak[0].Load() != 256 || stats.ReaderPeak[1].Load() != 2048 {
		t.Fatal("missing input peak", stats.Snapshot())
	}
	for index, count := range []int{256, 2048} {
		for n := 1; n <= count; n++ {
			p := <-channels[index]
			if binary.BigEndian.Uint32(p.data) != uint32(n) || p.at.Before(started) || p.at.After(time.Now()) {
				t.Fatal("handoff changed packet order, data or read time")
			}
		}
	}
	cancel()
}
