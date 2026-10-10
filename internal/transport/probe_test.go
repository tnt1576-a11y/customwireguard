package transport

import (
	"context"
	"testing"
	"time"

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

func TestFECPreflightCapabilityAndLegacyCompatibility(t *testing.T) {
	for _, serverFEC := range []bool{false, true} {
		for _, clientFEC := range []bool{false, true} {
			public, backend := socket(t), socket(t)
			cfg := config.Config{Mode: "server", Transport: "exchange", Listen: public.LocalAddr().String(), WireGuard: backend.LocalAddr().String(), Key: keyString(), Profile: "balanced", ExchangeFEC: serverFEC}
			stats := new(Stats)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- RunServer(ctx, public, cfg, stats) }()
			client := cfg
			client.Mode = "client"
			client.Endpoint = public.LocalAddr().String()
			client.ExchangeMaxPPS = 8000
			client.ExchangeFEC = clientFEC
			result, err := ProbeEndpoint(context.Background(), client, 1)
			cancel()
			select {
			case serverErr := <-done:
				if serverErr != nil {
					t.Fatal(serverErr)
				}
			case <-time.After(time.Second):
				t.Fatal("server cleanup timed out")
			}
			want := 1
			if clientFEC && !serverFEC {
				want = 0
			}
			if err != nil || result.Received != want {
				t.Fatalf("capability server=%t client=%t result=%+v error=%v", serverFEC, clientFEC, result, err)
			}
			if stats.Established.Load() != 0 || stats.Received.Load() != 0 {
				t.Fatal("preflight changed native/session state")
			}
		}
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
