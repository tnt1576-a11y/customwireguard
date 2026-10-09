package transport

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"customwireguard/internal/config"
)

func TestReplayPatternParserAndBounds(t *testing.T) {
	file := filepath.Join(t.TempDir(), "pattern.jsonl")
	text := `{"utc":"2026-10-09T18:00:00Z","event":"write","remote":"192.0.2.1:8443","bytes":257}` + "\n" +
		`{"utc":"2026-10-09T18:00:00Z","event":"read","remote":"192.0.2.1:8443","bytes":999}` + "\n" +
		`{"utc":"2026-10-09T18:00:01Z","event":"write","remote":"192.0.2.1:8443","bytes":1440}` + "\n"
	if err := os.WriteFile(file, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	points, err := LoadReplayPattern(file, "192.0.2.1:8443")
	if err != nil || len(points) != 2 || points[0].Size != 257 || points[1].After != time.Second {
		t.Fatalf("pattern: %v %v", points, err)
	}
	if _, err := LoadReplayPattern(file, "192.0.2.2:8443"); err == nil {
		t.Fatal("accepted wrong endpoint")
	}
	for _, p := range [][]ReplayPoint{nil, {{0, 256}}, {{0, 255}, {time.Second, 256}}, {{0, 256}, {time.Second, 1441}}, {{0, 256}, {time.Second, -1}}, {{time.Second, 256}, {2 * time.Second, 256}}, {{0, 256}, {91 * time.Second, 256}}, {{0, 256}, {2 * time.Second, 256}, {time.Second, 256}}} {
		if validateReplayPattern(p) == nil {
			t.Fatalf("accepted invalid pattern %v", p)
		}
	}
	burst := make([]ReplayPoint, 202)
	for i := range burst {
		burst[i] = ReplayPoint{0, 256}
	}
	burst[len(burst)-1].After = 3 * time.Second
	if validateReplayPattern(burst) == nil {
		t.Fatal("accepted excessive burst")
	}
}

func TestReplayProbeBurstAndPacedWithoutWireGuard(t *testing.T) {
	server, backend := socket(t), socket(t)
	stats := new(Stats)
	cfg := config.Config{Mode: "server", Listen: server.LocalAddr().String(), WireGuard: backend.LocalAddr().String(), Key: keyString(), Profile: "balanced"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- RunServer(ctx, server, cfg, stats, 200) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	client := config.Config{Mode: "client", Listen: "127.0.0.1:51821", WireGuard: "127.0.0.1:51822", Endpoint: cfg.Listen, Key: cfg.Key, Profile: cfg.Profile}
	var points []ReplayPoint
	for i := 0; i < 80; i++ {
		points = append(points, ReplayPoint{time.Duration(i/20) * 400 * time.Millisecond, 256 + i})
	}
	for _, style := range []string{"recorded", "paced"} {
		result, err := ReplayProbeEndpoint(ctx, client, points, style)
		if err != nil || result.Sent != 80 || result.Received != 80 || result.Rejected != 0 || result.Late != 0 {
			t.Fatalf("%s: %+v %v", style, result, err)
		}
		if points[1].After != 0 {
			t.Fatal("paced measurement mutated caller's pattern")
		}
	}
	if stats.Received.Load() != 0 || stats.Established.Load() != 2 {
		t.Fatal(fmt.Sprintf("Unexpected WG forwarding or sessions: %+v", stats.Snapshot()))
	}
}
