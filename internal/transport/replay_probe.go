package transport

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"customwireguard/internal/config"
	"customwireguard/internal/wire"
)

type ReplayPoint struct {
	After time.Duration
	Size  int
}
type ReplayProbeResult struct {
	Meaning          string            `json:"meaning"`
	Style            string            `json:"style"`
	Sent             int               `json:"sent"`
	Received         int               `json:"received"`
	Rejected         int               `json:"rejected"`
	Late             int               `json:"late"`
	LossPercent      float64           `json:"loss_percent"`
	MedianMS         float64           `json:"median_ms"`
	P95MS            float64           `json:"p95_ms"`
	SendDurationMS   float64           `json:"send_duration_ms"`
	MaxScheduleLagMS float64           `json:"max_schedule_lag_ms"`
	Stats            map[string]uint64 `json:"socket_stats"`
}

// LoadReplayPattern reads only the timing and lengths of successful outer writes.
// Captured ciphertext and hashes are not retransmitted. Bounds prevent an
// untrusted trace from scheduling an unbounded or excessive diagnostic.
func LoadReplayPattern(path, endpoint string) ([]ReplayPoint, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 4096), 4096)
	var points []ReplayPoint
	var first time.Time
	lines := 0
	for scan.Scan() {
		lines++
		if lines > 100000 {
			return nil, errors.New("trace exceeds 100000 records")
		}
		var record traceRecord
		if err := json.Unmarshal(scan.Bytes(), &record); err != nil {
			return nil, errors.New("invalid packet trace JSON")
		}
		if record.Event != "write" || record.Remote != endpoint {
			continue
		}
		now, err := time.Parse(time.RFC3339Nano, record.UTC)
		if err != nil {
			return nil, errors.New("invalid trace timestamp")
		}
		if first.IsZero() {
			first = now
		}
		points = append(points, ReplayPoint{After: now.Sub(first), Size: record.Bytes})
		if len(points) > 5000 {
			return nil, errors.New("pattern exceeds 5000 packets")
		}
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	return points, validateReplayPattern(points)
}

func validateReplayPattern(points []ReplayPoint) error {
	if len(points) < 2 || len(points) > 5000 || points[0].After != 0 {
		return errors.New("expected 2..5000 packets starting at zero")
	}
	duration := points[len(points)-1].After
	if duration < time.Second || duration > 90*time.Second {
		return errors.New("pattern must last 1..90 seconds")
	}
	if float64(len(points)-1)/duration.Seconds() > 100 {
		return errors.New("pattern average exceeds 100 packets/s")
	}
	budget := bucket{rate: 200, burst: 200}
	base := time.Unix(1000, 0)
	var previous time.Duration
	for _, p := range points {
		if p.After < previous || p.Size < 256 || p.Size > wire.MaxDatagram {
			return errors.New("pattern timestamps must increase and lengths must be 256..1440")
		}
		if !budget.allow(base.Add(p.After)) {
			return errors.New("pattern exceeds diagnostic burst budget")
		}
		previous = p.After
	}
	return nil
}

// ReplayProbeEndpoint reproduces packet lengths and timing using fresh encrypted
// Ping messages. The server must explicitly allow 200 diagnostic replies/s.
// It owns the outer session, but never starts WireGuard or sends DATA frames.
// The immediate mirrored replies do not reproduce independent downstream traffic.
func ReplayProbeEndpoint(ctx context.Context, cfg config.Config, points []ReplayPoint, style string) (ReplayProbeResult, error) {
	result := ReplayProbeResult{Style: style, Meaning: "UDP-only pattern measurement without WireGuard or routing changes. Fresh encrypted Ping/Pong messages reproduce outbound sizes/timing, not captured contents. Mirrored replies do not reproduce independent downstream traffic. Server diagnostic reply allowance must be 200/s. Stop the normal client first."}
	if err := cfg.Validate("client"); err != nil {
		return result, err
	}
	if err := validateReplayPattern(points); err != nil {
		return result, err
	}
	if style != "recorded" && style != "paced" {
		return result, errors.New("style must be recorded or paced")
	}
	schedule := append([]ReplayPoint(nil), points...)
	if style == "paced" {
		duration := schedule[len(schedule)-1].After
		for i := range schedule {
			schedule[i].After = time.Duration(i) * duration / time.Duration(len(schedule)-1)
		}
	}
	conn, active, err := dialProbeSession(ctx, cfg)
	if err != nil {
		return result, err
	}
	defer conn.Close()
	stats := new(Stats)
	stats.Established.Add(1)
	channels, cleanup := readers(ctx, stats, conn)
	defer cleanup()
	pending := make(map[uint64]time.Time)
	var rtts []float64
	started := time.Now()
	sendTimer := time.NewTimer(0)
	defer sendTimer.Stop()
	drain := time.NewTimer(time.Hour)
	drain.Stop()
	defer drain.Stop()
	var finished <-chan time.Time
	finish := func() ReplayProbeResult {
		result.Stats = stats.Snapshot()
		if result.Sent > 0 {
			result.LossPercent = 100 * float64(result.Sent-result.Received) / float64(result.Sent)
		}
		if len(rtts) > 0 {
			sort.Float64s(rtts)
			result.MedianMS = rtts[len(rtts)/2]
			if len(rtts)%2 == 0 {
				result.MedianMS = (rtts[len(rtts)/2-1] + rtts[len(rtts)/2]) / 2
			}
			result.P95MS = rtts[(95*len(rtts)+99)/100-1]
		}
		return result
	}
	for {
		select {
		case <-ctx.Done():
			return finish(), ctx.Err()
		case <-finished:
			return finish(), nil
		case <-sendTimer.C:
			now := time.Now()
			point := schedule[result.Sent]
			lag := float64(now.Sub(started.Add(point.After))) / float64(time.Millisecond)
			if lag > result.MaxScheduleLagMS {
				result.MaxScheduleLagMS = lag
			}
			active.send++
			payload := make([]byte, 8)
			binary.BigEndian.PutUint64(payload, active.send)
			out, e := active.codec.Seal(wire.Frame{Kind: wire.Ping, ID: active.id, Sequence: active.send, Timestamp: now.Unix(), Payload: payload}, point.Size)
			if e != nil {
				return finish(), e
			}
			if _, e = conn.Write(out); e != nil {
				return finish(), fmt.Errorf("send pattern probe: %w", e)
			}
			pending[active.send] = now
			result.Sent++
			if result.Sent == len(schedule) {
				result.SendDurationMS = float64(time.Since(started)) / float64(time.Millisecond)
				drain.Reset(2 * time.Second)
				finished = drain.C
			} else {
				sendTimer.Reset(max(0, time.Until(started.Add(schedule[result.Sent].After))))
			}
		case p := <-channels[0]:
			now := time.Now()
			f, ok, _ := active.open(p.data, now)
			if !ok {
				result.Rejected++
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
				result.Late++
				continue
			}
			result.Received++
			rtts = append(rtts, float64(rtt)/float64(time.Millisecond))
		}
	}
}
