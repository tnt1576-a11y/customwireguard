package transport

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sort"
	"time"

	"customwireguard/internal/config"
	"customwireguard/internal/wire"
)

type SessionProbeStage struct {
	Rate        int     `json:"packets_per_second"`
	Sent        int     `json:"sent"`
	Received    int     `json:"received"`
	Late        int     `json:"late"`
	Rejected    int     `json:"rejected"`
	LossPercent float64 `json:"loss_percent"`
	MedianMS    float64 `json:"median_ms"`
	P95MS       float64 `json:"p95_ms"`
}

type SessionProbeResult struct {
	Meaning string              `json:"meaning"`
	Stages  []SessionProbeStage `json:"stages"`
	Stats   map[string]uint64   `json:"socket_stats"`
}

// SessionProbeEndpoint exercises the existing authenticated Ping/Pong messages.
// Unlike the bootstrap probe it takes ownership of the single active session:
// the normal client must be stopped first. It never sends WireGuard DATA frames.
// The maximum rate stays within the v1 server's 20/s Ping response limit; quiet
// drain intervals replenish its burst allowance between stages.
func SessionProbeEndpoint(ctx context.Context, cfg config.Config) (SessionProbeResult, error) {
	return sessionProbeEndpoint(ctx, cfg, []int{1, 5, 10, 20}, time.Second*8)
}

func sessionProbeEndpoint(ctx context.Context, cfg config.Config, rates []int, duration time.Duration) (SessionProbeResult, error) {
	result := SessionProbeResult{Meaning: "Authenticated outer session only, without WireGuard or route changes. Replaces the server's active CWG session; stop the normal client first. Rates are capped at the existing server's 20/s limit."}
	if err := cfg.Validate("client"); err != nil {
		return result, err
	}
	if len(rates) == 0 || len(rates) > 4 || duration < time.Second || duration > 10*time.Second || duration%time.Second != 0 {
		return result, errors.New("expected 1..4 stages of 1..10 whole seconds")
	}
	for _, rate := range rates {
		if rate < 1 || rate > 20 {
			return result, errors.New("session probe rate must be 1..20 packets/s")
		}
	}
	conn, active, err := dialProbeSession(ctx, cfg)
	if err != nil {
		return result, err
	}
	defer conn.Close()
	stats := new(Stats)
	channels, cleanup := readers(ctx, stats, conn)
	defer cleanup()
	for _, rate := range rates {
		stage, err := runSessionProbeStage(ctx, conn, channels[0], active, rate, duration)
		result.Stages = append(result.Stages, stage)
		result.Stats = stats.Snapshot()
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

func dialProbeSession(ctx context.Context, cfg config.Config) (*net.UDPConn, *association, error) {
	key, _ := cfg.Secret()
	bootstrap, err := wire.NewCodec(key, nil, true)
	if err != nil {
		return nil, nil, err
	}
	endpoint, err := net.ResolveUDPAddr("udp", cfg.Endpoint)
	if err != nil {
		return nil, nil, err
	}
	conn, err := net.DialUDP("udp", nil, endpoint)
	if err != nil {
		return nil, nil, err
	}
	ready := false
	defer func() {
		if !ready {
			conn.Close()
		}
	}()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	conn.SetReadBuffer(2 << 20)
	conn.SetWriteBuffer(2 << 20)
	id := wire.NewID()
	var active *association
	buf := make([]byte, 65535)
	for attempt := 0; attempt < 3 && active == nil; attempt++ {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		now := time.Now()
		out, err := bootstrap.Seal(wire.Frame{Kind: wire.Hello, ID: id, Timestamp: now.Unix()}, 384)
		if err != nil {
			return nil, nil, err
		}
		conn.SetDeadline(now.Add(2 * time.Second))
		if _, err = conn.Write(out); err != nil {
			return nil, nil, err
		}
		for {
			n, readErr := conn.Read(buf)
			if readErr != nil {
				if netErr, ok := readErr.(net.Error); ok && netErr.Timeout() {
					break
				}
				return nil, nil, readErr
			}
			f, openErr := bootstrap.Open(buf[:n], time.Now())
			if openErr != nil || f.Kind != wire.Welcome || f.ID != id || f.Sequence != 0 || len(f.Payload) != 32 || n < 256 || n > 512 {
				continue
			}
			active, err = newAssociation(key, id, f.Payload, true, time.Now())
			if err != nil {
				return nil, nil, err
			}
			break
		}
	}
	if active == nil {
		return nil, nil, errors.New("no authenticated session welcome received")
	}
	conn.SetDeadline(time.Time{})
	ready = true
	return conn, active, nil
}

func runSessionProbeStage(ctx context.Context, conn *net.UDPConn, packets <-chan packet, active *association, rate int, duration time.Duration) (SessionProbeStage, error) {
	stage := SessionProbeStage{Rate: rate}
	target := rate * int(duration/time.Second)
	pending := make(map[uint64]time.Time, target)
	var rtts []float64
	sizes := []int{256, 257, 512, 513, 1024, 1025, 1439, 1440}
	ticker := time.NewTicker(time.Second / time.Duration(rate))
	defer ticker.Stop()
	drain := time.NewTimer(time.Hour)
	drain.Stop()
	defer drain.Stop()
	var finished <-chan time.Time
	finish := func() SessionProbeStage {
		if stage.Sent > 0 {
			stage.LossPercent = 100 * float64(stage.Sent-stage.Received) / float64(stage.Sent)
		}
		if len(rtts) > 0 {
			sort.Float64s(rtts)
			stage.MedianMS = rtts[len(rtts)/2]
			if len(rtts)%2 == 0 {
				stage.MedianMS = (rtts[len(rtts)/2-1] + rtts[len(rtts)/2]) / 2
			}
			stage.P95MS = rtts[(95*len(rtts)+99)/100-1]
		}
		return stage
	}
	for {
		select {
		case <-ctx.Done():
			return finish(), ctx.Err()
		case <-finished:
			return finish(), nil
		case <-ticker.C:
			now := time.Now()
			active.send++
			payload := make([]byte, 8)
			binary.BigEndian.PutUint64(payload, active.send)
			out, err := active.codec.Seal(wire.Frame{Kind: wire.Ping, ID: active.id, Sequence: active.send, Timestamp: now.Unix(), Payload: payload}, sizes[stage.Sent%len(sizes)])
			if err != nil {
				return finish(), err
			}
			if _, err = conn.Write(out); err != nil {
				return finish(), fmt.Errorf("send session probe: %w", err)
			}
			pending[active.send] = now
			stage.Sent++
			if stage.Sent == target {
				ticker.Stop()
				drain.Reset(2 * time.Second)
				finished = drain.C
			}
		case p := <-packets:
			now := time.Now()
			f, ok, _ := active.open(p.data, now)
			if !ok {
				stage.Rejected++
				continue
			}
			if f.Kind != wire.Pong {
				continue
			}
			token := binary.BigEndian.Uint64(f.Payload)
			sent, exists := pending[token]
			if !exists {
				continue
			}
			delete(pending, token)
			rtt := now.Sub(sent)
			if rtt > 2*time.Second {
				stage.Late++
				continue
			}
			stage.Received++
			rtts = append(rtts, float64(rtt)/float64(time.Millisecond))
		}
	}
}
