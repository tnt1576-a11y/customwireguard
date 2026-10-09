package transport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPacketTraceSocketBoundaries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "packets.jsonl")
	trace, err := OpenPacketTrace(path)
	if err != nil {
		t.Fatal(err)
	}
	defer trace.Close()
	if other, err := OpenPacketTrace(path); err == nil {
		other.Close()
		t.Fatal("overwrote existing trace")
	}
	sender, receiver := socket(t), socket(t)
	stats := &Stats{Trace: trace}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	channels, cleanup := readers(ctx, stats, receiver)
	defer cleanup()
	payload := []byte("never store these private packet bytes")
	for i := 0; i < 10; i++ {
		if !send(sender, receiver.LocalAddr().(*net.UDPAddr).AddrPort(), payload, stats) {
			t.Fatal("send failed")
		}
		select {
		case p := <-channels[0]:
			if !bytes.Equal(p.data, payload) {
				t.Fatal("trace changed the datagram")
			}
		case <-time.After(time.Second):
			t.Fatal("missing localhost datagram")
		}
	}
	if err := trace.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, payload) {
		t.Fatal("trace stored packet content")
	}
	digest := sha256.Sum256(payload)
	counts := map[string]int{}
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte("\n")) {
		var record traceRecord
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		if record.SHA256 != hex.EncodeToString(digest[:]) || record.Bytes != len(payload) || record.Local == "" || record.Remote == "" {
			t.Fatalf("wrong boundary metadata: %+v", record)
		}
		counts[record.Event]++
	}
	if counts["write"] != 10 || counts["read"] != 10 {
		t.Fatalf("boundaries: %v", counts)
	}
}

func TestPacketTraceBoundedAndClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bounded.jsonl")
	trace, err := OpenPacketTrace(path)
	if err != nil {
		t.Fatal(err)
	}
	trace.remaining = 2
	conn := socket(t)
	for i := 0; i < 10; i++ {
		trace.record("read", conn, zeroAddr, []byte{1, 2, 3})
	}
	if err := trace.Close(); err != nil {
		t.Fatal(err)
	}
	trace.record("read", conn, zeroAddr, []byte{4}) // Closing must be safe.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(raw, []byte("\n")) != 3 || bytes.Count(raw, []byte("limit_reached")) != 1 {
		t.Fatal("trace did not stop at its record limit")
	}
}
