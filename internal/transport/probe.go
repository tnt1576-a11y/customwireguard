package transport

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"sort"
	"time"

	"customwireguard/internal/config"
	"customwireguard/internal/wire"
)

type ProbeResult struct {
	Sent        int           `json:"sent"`
	Received    int           `json:"received"`
	LossPercent float64       `json:"loss_percent"`
	MedianMS    float64       `json:"median_ms"`
	P95MS       float64       `json:"p95_ms"`
	Meaning     string        `json:"meaning"`
	Samples     []ProbeSample `json:"samples"`
}

type ProbeSample struct {
	Bytes    int     `json:"udp_payload_bytes"`
	Received bool    `json:"received"`
	RTTMS    float64 `json:"rtt_ms,omitempty"`
}

// Probe uses a separate authenticated diagnostic type. It never replaces the
// client's live association or forwards anything to the WireGuard backend.
func ProbeEndpoint(ctx context.Context, cfg config.Config, count int, sizes ...int) (ProbeResult, error) {
	result := ProbeResult{Meaning: "Outer UDP transport only; does not verify WireGuard, routing, DNS, or long-term DPI resistance."}
	if err := cfg.Validate("client"); err != nil {
		return result, err
	}
	if count < 1 || count > 100 {
		return result, fmt.Errorf("probe count must be 1..100")
	}
	if len(sizes) == 0 {
		sizes = []int{256, 512, 1024, wire.MaxDatagram}
	}
	if len(sizes) > 32 {
		return result, fmt.Errorf("at most 32 probe sizes are supported")
	}
	for _, size := range sizes {
		if size < 256 || size > wire.MaxDatagram {
			return result, fmt.Errorf("probe sizes must be 256..%d bytes", wire.MaxDatagram)
		}
	}
	key, _ := cfg.Secret()
	codec, err := codecForMode(key, nil, true, cfg.Transport == "exchange")
	if err != nil {
		return result, err
	}
	addr, err := net.ResolveUDPAddr("udp", cfg.Endpoint)
	if err != nil {
		return result, err
	}
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return result, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	buf := make([]byte, 65535)
	var samples []float64
	for i := 0; i < count; i++ {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		id := wire.NewID()
		start := time.Now()
		size := sizes[i%len(sizes)]
		sample := ProbeSample{Bytes: size}
		capability := fecBootstrap(cfg.ExchangeFEC)
		out, e := codec.Seal(wire.Frame{Kind: wire.Probe, ID: id, Timestamp: start.Unix(), Payload: capability}, size)
		if e != nil {
			return result, e
		}
		conn.SetDeadline(start.Add(2 * time.Second))
		result.Sent++
		if _, e = conn.Write(out); e == nil {
			for {
				n, readErr := conn.Read(buf)
				if readErr != nil {
					break
				}
				f, openErr := codec.Open(buf[:n], time.Now())
				if openErr == nil && f.Kind == wire.ProbeReply && f.ID == id && f.Sequence == 0 && bytes.Equal(f.Payload, capability) && n == len(out) {
					result.Received++
					sample.Received = true
					sample.RTTMS = float64(time.Since(start)) / float64(time.Millisecond)
					samples = append(samples, sample.RTTMS)
					break
				}
			}
		}
		result.Samples = append(result.Samples, sample)
		if i < count-1 {
			select {
			case <-ctx.Done():
				return result, ctx.Err()
			case <-time.After(time.Duration(wire.RandomBetween(650, 1100)) * time.Millisecond):
			}
		}
	}
	result.LossPercent = 100 * float64(result.Sent-result.Received) / float64(result.Sent)
	if len(samples) > 0 {
		sort.Float64s(samples)
		result.MedianMS = samples[len(samples)/2]
		if len(samples)%2 == 0 {
			result.MedianMS = (samples[len(samples)/2-1] + samples[len(samples)/2]) / 2
		}
		result.P95MS = samples[(95*len(samples)+99)/100-1]
	}
	return result, nil
}
