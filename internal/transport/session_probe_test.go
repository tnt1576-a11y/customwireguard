package transport

import (
	"context"
	"testing"
	"time"

	"customwireguard/internal/config"
)

func TestSessionProbeRatesWithoutWireGuard(t *testing.T) {
	f := serverFixture(t)
	cfg := config.Config{Mode: "client", Listen: "127.0.0.1:51821", WireGuard: "127.0.0.1:51822", Endpoint: f.server.LocalAddr().String(), Key: f.cfg.Key, Profile: "balanced"}
	result, err := sessionProbeEndpoint(context.Background(), cfg, []int{1, 20}, time.Second)
	if err != nil || len(result.Stages) != 2 {
		t.Fatalf("session probe: %+v, %v", result, err)
	}
	for _, stage := range result.Stages {
		if stage.Sent != stage.Rate || stage.Received != stage.Sent || stage.Rejected != 0 || stage.Late != 0 {
			t.Fatalf("stage loss below server rate limit: %+v", stage)
		}
	}
	if f.stats.Established.Load() != 1 || f.stats.Received.Load() != 0 {
		t.Fatal("expected one outer session and no DATA forwarded to WireGuard")
	}
}

func TestSessionProbeRejectsUnboundedRate(t *testing.T) {
	cfg := config.Config{Mode: "client", Listen: "127.0.0.1:51821", WireGuard: "127.0.0.1:51822", Endpoint: "127.0.0.1:443", Key: keyString(), Profile: "balanced"}
	for _, rates := range [][]int{nil, {0}, {21}, {1, 1, 1, 1, 1}} {
		if _, err := sessionProbeEndpoint(context.Background(), cfg, rates, time.Second); err == nil {
			t.Fatalf("accepted invalid rates %v", rates)
		}
	}
	for _, duration := range []time.Duration{0, 1500 * time.Millisecond, 11 * time.Second} {
		if _, err := sessionProbeEndpoint(context.Background(), cfg, []int{1}, duration); err == nil {
			t.Fatalf("accepted invalid duration %v", duration)
		}
	}
}
