package transport

import (
	"context"
	"testing"

	"customwireguard/internal/config"
)

func TestProbeAdjacentSizes(t *testing.T) {
	f := serverFixture(t)
	cfg := config.Config{Mode: "client", Listen: "127.0.0.1:51821", WireGuard: "127.0.0.1:51822", Endpoint: f.server.LocalAddr().String(), Key: f.cfg.Key, Profile: "balanced"}
	sizes := []int{257, 258, 1025, 1439}
	result, err := ProbeEndpoint(context.Background(), cfg, len(sizes), sizes...)
	if err != nil || result.Received != len(sizes) || len(result.Samples) != len(sizes) {
		t.Fatalf("adjacent-size round trips: %+v, %v", result, err)
	}
	for i, sample := range result.Samples {
		if !sample.Received || sample.Bytes != sizes[i] {
			t.Fatalf("sample %d: %+v", i, sample)
		}
	}
	if f.stats.Established.Load() != 0 || f.stats.Received.Load() != 0 {
		t.Fatal("probes created an association or forwarded data to WireGuard")
	}
}

func TestProbeRejectsInvalidSizes(t *testing.T) {
	cfg := config.Config{Mode: "client", Listen: "127.0.0.1:51821", WireGuard: "127.0.0.1:51822", Endpoint: "127.0.0.1:443", Key: keyString(), Profile: "balanced"}
	for _, sizes := range [][]int{{255}, {1441}, {-1}, {256, 1441}, make([]int, 33)} {
		result, err := ProbeEndpoint(context.Background(), cfg, 1, sizes...)
		if err == nil || result.Sent != 0 {
			t.Fatalf("invalid sizes accepted/sent: %v, %+v, %v", sizes, result, err)
		}
	}
}
