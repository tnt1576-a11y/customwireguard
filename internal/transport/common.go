package transport

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"customwireguard/internal/wire"
)

type Stats struct {
	// Set before starting any readers; immutable for the run. Nil disables it.
	Trace        *PacketTrace
	Established  atomic.Uint64
	Sent         atomic.Uint64
	Received     atomic.Uint64
	Rejected     atomic.Uint64
	Oversize     atomic.Uint64
	QueueDropped atomic.Uint64
	SocketErrors atomic.Uint64
}

func (s *Stats) Snapshot() map[string]uint64 {
	return map[string]uint64{"sessions": s.Established.Load(), "data_sent": s.Sent.Load(),
		"data_received": s.Received.Load(), "rejected": s.Rejected.Load(), "oversize": s.Oversize.Load(),
		"queue_dropped": s.QueueDropped.Load(), "socket_errors": s.SocketErrors.Load()}
}

type packet struct {
	data []byte
	addr netip.AddrPort
}

// One fixed read buffer per socket; only bounded, correctly sized packets enter
// the queue. Socket errors (including ICMP port-unreachable) do not kill retries.
func pump(ctx context.Context, conn *net.UDPConn, out chan<- packet, stats *Stats, wg *sync.WaitGroup) {
	defer wg.Done()
	buf := make([]byte, 65535)
	for {
		n, addr, err := conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			stats.SocketErrors.Add(1)
			select {
			case <-ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}
		if n > wire.MaxDatagram {
			stats.Oversize.Add(1)
			continue
		}
		stats.Trace.record("read", conn, addr, buf[:n])
		p := packet{append([]byte(nil), buf[:n]...), addr}
		select {
		case out <- p:
		case <-ctx.Done():
			return
		default:
			stats.QueueDropped.Add(1)
		}
	}
}

func readers(ctx context.Context, stats *Stats, conns ...*net.UDPConn) ([]chan packet, func()) {
	var wg sync.WaitGroup
	channels := make([]chan packet, len(conns))
	stop := context.AfterFunc(ctx, func() {
		for _, c := range conns {
			c.Close()
		}
	})
	for i, c := range conns {
		channels[i] = make(chan packet, 256)
		wg.Add(1)
		go pump(ctx, c, channels[i], stats, &wg)
	}
	return channels, func() {
		stop()
		for _, c := range conns {
			c.Close()
		}
		wg.Wait()
	}
}

type association struct {
	id          wire.ID
	codec       *wire.Codec
	token       []byte
	addr        netip.AddrPort
	send        uint64
	replay      wire.ReplayWindow
	lastSeen    time.Time
	created     time.Time
	lastWelcome time.Time
}

func newAssociation(key []byte, id wire.ID, token []byte, client bool, now time.Time) (*association, error) {
	material := append(append([]byte(nil), id[:]...), token...)
	codec, err := wire.NewCodec(key, material, client)
	if err != nil {
		return nil, err
	}
	return &association{id: id, codec: codec, token: append([]byte(nil), token...), created: now, lastSeen: now}, nil
}

func (a *association) seal(kind byte, payload []byte, profile string, now time.Time) ([]byte, error) {
	if a.send == math.MaxUint64 {
		return nil, errors.New("session sequence exhausted")
	}
	size, err := wire.PacketSize(len(payload), profile)
	if err != nil {
		return nil, err
	}
	a.send++
	return a.codec.Seal(wire.Frame{Kind: kind, ID: a.id, Sequence: a.send, Timestamp: now.Unix(), Payload: payload}, size)
}

func (a *association) open(p []byte, now time.Time) (wire.Frame, bool, bool) {
	f, err := a.codec.Open(p, now)
	if err != nil || f.ID != a.id {
		return wire.Frame{}, false, false
	}
	if f.Kind != wire.Data && f.Kind != wire.Ping && f.Kind != wire.Pong {
		return wire.Frame{}, false, false
	}
	if (f.Kind == wire.Ping || f.Kind == wire.Pong) && len(f.Payload) != 8 {
		return wire.Frame{}, false, false
	}
	if f.Kind == wire.Data && len(f.Payload) == 0 {
		return wire.Frame{}, false, false
	}
	accepted, newest := a.replay.Accept(f.Sequence)
	if !accepted {
		return wire.Frame{}, false, false
	}
	a.lastSeen = now
	return f, true, newest
}

func send(conn *net.UDPConn, addr netip.AddrPort, data []byte, stats *Stats) bool {
	var err error
	if addr.IsValid() {
		_, err = conn.WriteToUDPAddrPort(data, addr)
	} else {
		_, err = conn.Write(data)
	}
	if err != nil {
		stats.SocketErrors.Add(1)
		stats.Trace.record("write_error", conn, addr, data)
		return false
	}
	stats.Trace.record("write", conn, addr, data)
	return true
}

func pingPayload() []byte {
	id := wire.NewID()
	return append([]byte(nil), id[:8]...)
}

// bucket bounds bootstrap and diagnostic work, independently of established data.
type bucket struct {
	tokens float64
	rate   float64
	burst  float64
	last   time.Time
}

func (b *bucket) allow(now time.Time) bool {
	if b.last.IsZero() {
		b.tokens = b.burst
		b.last = now
	}
	b.tokens = math.Min(b.burst, b.tokens+now.Sub(b.last).Seconds()*b.rate)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func tokenNumber(p []byte) uint64 { return binary.BigEndian.Uint64(p) }
