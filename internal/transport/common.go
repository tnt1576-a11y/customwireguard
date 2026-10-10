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
	Trace                      *PacketTrace
	Established                atomic.Uint64
	Sent                       atomic.Uint64
	Received                   atomic.Uint64
	Rejected                   atomic.Uint64
	Oversize                   atomic.Uint64
	QueueDropped               atomic.Uint64
	ReaderDropped              [2]atomic.Uint64
	ReaderPeak                 [2]atomic.Uint64
	SocketErrors               atomic.Uint64
	PacingQueued               atomic.Uint64
	PacingDropped              atomic.Uint64
	PacingQueueBytes           atomic.Uint64
	PacingPeakBytes            atomic.Uint64
	PacingWaitUS               atomic.Uint64
	ExchangeRequests           atomic.Uint64
	ExchangeResumes            atomic.Uint64
	ExchangeReplies            atomic.Uint64
	ExchangeEmpty              atomic.Uint64
	ExchangeExpired            atomic.Uint64
	ExchangeQueueDropped       atomic.Uint64
	ExchangePending            atomic.Uint64
	ExchangeWindowFull         atomic.Uint64
	ExchangeQueueExpired       atomic.Uint64
	ExchangeQueueFull          atomic.Uint64
	ExchangeSmallQueueFull     atomic.Uint64
	ExchangeBulkQueueFull      atomic.Uint64
	ExchangeControlQueueFull   atomic.Uint64
	ExchangeControlExpired     atomic.Uint64
	ExchangeControlSent        atomic.Uint64
	ExchangeQueuePackets       atomic.Uint64
	ExchangeQueuePeak          atomic.Uint64
	ExchangeQueueDelayMaxUS    atomic.Uint64
	ExchangeRatePPS            atomic.Uint64
	ExchangeRateIncreases      atomic.Uint64
	ExchangeBackoffs           atomic.Uint64
	ExchangeLossBackoffs       atomic.Uint64
	ExchangeDelayBackoffs      atomic.Uint64
	ExchangePriorFlightReplies atomic.Uint64
	ExchangePriorFlightExpired atomic.Uint64
	ExchangeRTTUS              atomic.Uint64
	ExchangeLateReplies        atomic.Uint64
	ExchangeReorderedReplies   atomic.Uint64
	ExchangeReorderedRequests  atomic.Uint64
	ExchangeReorderHeld        atomic.Uint64
	ExchangeReorderLate        atomic.Uint64
	ExchangeReorderForced      atomic.Uint64
	ExchangeReorderWaitMaxUS   atomic.Uint64
	ExchangeRepairSent         atomic.Uint64
	ExchangeRepairExpired      atomic.Uint64
	ExchangeFECData            atomic.Uint64
	ExchangeFECParity          atomic.Uint64
	ExchangeFECRecovered       atomic.Uint64
	ExchangeFECExpired         atomic.Uint64
	ExchangeFECDeferred        atomic.Uint64
	ExchangeFECSkipped         atomic.Uint64
	ExchangeFECInvalid         atomic.Uint64
	ExchangeFECLate            atomic.Uint64
	ExchangeSmallQueuePackets  atomic.Uint64
	ExchangeSmallSent          atomic.Uint64
	ExchangeSmallDelayMaxUS    atomic.Uint64
}

func (s *Stats) Snapshot() map[string]uint64 {
	return map[string]uint64{"sessions": s.Established.Load(), "data_sent": s.Sent.Load(),
		"data_received": s.Received.Load(), "rejected": s.Rejected.Load(), "oversize": s.Oversize.Load(),
		"queue_dropped": s.QueueDropped.Load(), "socket_errors": s.SocketErrors.Load(),
		"reader_0_dropped": s.ReaderDropped[0].Load(), "reader_1_dropped": s.ReaderDropped[1].Load(),
		"reader_0_peak": s.ReaderPeak[0].Load(), "reader_1_peak": s.ReaderPeak[1].Load(),
		"pacing_queued": s.PacingQueued.Load(), "pacing_dropped": s.PacingDropped.Load(),
		"pacing_queue_bytes": s.PacingQueueBytes.Load(), "pacing_peak_bytes": s.PacingPeakBytes.Load(),
		"pacing_wait_us": s.PacingWaitUS.Load(), "exchange_requests": s.ExchangeRequests.Load(),
		"exchange_replies": s.ExchangeReplies.Load(), "exchange_empty": s.ExchangeEmpty.Load(),
		"exchange_expired": s.ExchangeExpired.Load(), "exchange_queue_dropped": s.ExchangeQueueDropped.Load(),
		"exchange_resumes": s.ExchangeResumes.Load(),
		"exchange_pending": s.ExchangePending.Load(), "exchange_window_full": s.ExchangeWindowFull.Load(),
		"exchange_queue_expired": s.ExchangeQueueExpired.Load(), "exchange_queue_full": s.ExchangeQueueFull.Load(),
		"exchange_small_queue_full": s.ExchangeSmallQueueFull.Load(), "exchange_bulk_queue_full": s.ExchangeBulkQueueFull.Load(),
		"exchange_control_queue_full": s.ExchangeControlQueueFull.Load(), "exchange_control_expired": s.ExchangeControlExpired.Load(), "exchange_control_sent": s.ExchangeControlSent.Load(),
		"exchange_queue_packets": s.ExchangeQueuePackets.Load(), "exchange_queue_peak": s.ExchangeQueuePeak.Load(),
		"exchange_queue_delay_max_us": s.ExchangeQueueDelayMaxUS.Load(), "exchange_rate_pps": s.ExchangeRatePPS.Load(),
		"exchange_rate_increases": s.ExchangeRateIncreases.Load(), "exchange_backoffs": s.ExchangeBackoffs.Load(),
		"exchange_loss_backoffs": s.ExchangeLossBackoffs.Load(), "exchange_delay_backoffs": s.ExchangeDelayBackoffs.Load(),
		"exchange_prior_flight_replies": s.ExchangePriorFlightReplies.Load(), "exchange_prior_flight_expired": s.ExchangePriorFlightExpired.Load(),
		"exchange_rtt_us": s.ExchangeRTTUS.Load(), "exchange_late_replies": s.ExchangeLateReplies.Load(),
		"exchange_reordered_replies": s.ExchangeReorderedReplies.Load(),
		"exchange_repair_sent":       s.ExchangeRepairSent.Load(), "exchange_repair_expired": s.ExchangeRepairExpired.Load(),
		"exchange_fec_data": s.ExchangeFECData.Load(), "exchange_fec_parity": s.ExchangeFECParity.Load(),
		"exchange_fec_recovered": s.ExchangeFECRecovered.Load(), "exchange_fec_expired": s.ExchangeFECExpired.Load(),
		"exchange_fec_deferred": s.ExchangeFECDeferred.Load(),
		"exchange_fec_skipped":  s.ExchangeFECSkipped.Load(), "exchange_fec_invalid": s.ExchangeFECInvalid.Load(), "exchange_fec_late": s.ExchangeFECLate.Load(),
		"exchange_reordered_requests": s.ExchangeReorderedRequests.Load(),
		"exchange_reorder_held":       s.ExchangeReorderHeld.Load(), "exchange_reorder_late": s.ExchangeReorderLate.Load(),
		"exchange_reorder_forced": s.ExchangeReorderForced.Load(), "exchange_reorder_wait_max_us": s.ExchangeReorderWaitMaxUS.Load(),
		"exchange_small_queue_packets": s.ExchangeSmallQueuePackets.Load(),
		"exchange_small_sent":          s.ExchangeSmallSent.Load(), "exchange_small_delay_max_us": s.ExchangeSmallDelayMaxUS.Load()}
}

func storeMax(dst *atomic.Uint64, value uint64) {
	for old := dst.Load(); value > old; old = dst.Load() {
		if dst.CompareAndSwap(old, value) {
			return
		}
	}
}

type packet struct {
	data []byte
	addr netip.AddrPort
	at   time.Time
}

// A real UDP socket or an isolated user's view of a shared public socket.
// The view preserves source addresses and owns only its own cancellation.
type datagramSocket interface {
	ReadFromUDPAddrPort([]byte) (int, netip.AddrPort, error)
	WriteToUDPAddrPort([]byte, netip.AddrPort) (int, error)
	Write([]byte) (int, error)
	LocalAddr() net.Addr
	RemoteAddr() net.Addr
	Close() error
}

// One fixed read buffer per socket; only bounded, correctly sized packets enter
// the queue. Socket errors (including ICMP port-unreachable) do not kill retries.
func pump(ctx context.Context, conn datagramSocket, out chan<- packet, stats *Stats, index int, wg *sync.WaitGroup) {
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
		at := time.Now()
		if timed, ok := conn.(interface{ receivedAt() time.Time }); ok {
			at = timed.receivedAt()
		}
		stats.Trace.record("read", conn, addr, buf[:n])
		p := packet{data: append([]byte(nil), buf[:n]...), addr: addr, at: at}
		select {
		case out <- p:
			if index < len(stats.ReaderPeak) {
				storeMax(&stats.ReaderPeak[index], uint64(len(out)))
			}
		case <-ctx.Done():
			return
		default:
			stats.QueueDropped.Add(1)
			if index < len(stats.ReaderDropped) {
				stats.ReaderDropped[index].Add(1)
			}
		}
	}
}

func readers(ctx context.Context, stats *Stats, conns ...datagramSocket) ([]chan packet, func()) {
	return readersSized(ctx, stats, nil, conns...)
}

// Callers can size individual bounded handoffs for coalesced receive bursts.
// Packet ages begin at the socket read, so increasing a handoff cannot reset
// downstream deadlines. Omitted sizes retain the 256-packet default.
func readersSized(ctx context.Context, stats *Stats, capacities []int, conns ...datagramSocket) ([]chan packet, func()) {
	var wg sync.WaitGroup
	channels := make([]chan packet, len(conns))
	stop := context.AfterFunc(ctx, func() {
		for _, c := range conns {
			c.Close()
		}
	})
	for i, c := range conns {
		capacity := 256
		if i < len(capacities) {
			capacity = capacities[i]
		}
		channels[i] = make(chan packet, capacity)
		wg.Add(1)
		go pump(ctx, c, channels[i], stats, i, &wg)
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
	exchange    bool
	fec         bool
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
	return newAssociationMode(key, id, token, client, now, false)
}

func codecForMode(key, material []byte, client, exchange bool) (*wire.Codec, error) {
	if exchange {
		return wire.NewExchangeCodec(key, material, client)
	}
	return wire.NewCodec(key, material, client)
}

func newAssociationMode(key []byte, id wire.ID, token []byte, client bool, now time.Time, exchange bool) (*association, error) {
	material := append(append([]byte(nil), id[:]...), token...)
	codec, err := codecForMode(key, material, client, exchange)
	if err != nil {
		return nil, err
	}
	return &association{id: id, codec: codec, token: append([]byte(nil), token...), created: now, lastSeen: now, exchange: exchange}, nil
}

func (a *association) seal(kind byte, payload []byte, profile string, now time.Time) ([]byte, error) {
	if a.send == math.MaxUint64 {
		return nil, errors.New("session sequence exhausted")
	}
	size, err := wire.PacketSize(len(payload), profile)
	if err != nil {
		return nil, err
	}
	return a.sealSized(kind, payload, size, now)
}

func (a *association) sealSized(kind byte, payload []byte, size int, now time.Time) ([]byte, error) {
	if a.send == math.MaxUint64 {
		return nil, errors.New("session sequence exhausted")
	}
	a.send++
	return a.codec.Seal(wire.Frame{Kind: kind, ID: a.id, Sequence: a.send, Timestamp: now.Unix(), Payload: payload}, size)
}

func (a *association) open(p []byte, now time.Time) (wire.Frame, bool, bool) {
	f, err := a.codec.Open(p, now)
	if err != nil || f.ID != a.id {
		return wire.Frame{}, false, false
	}
	if a.exchange {
		if !((f.Kind == wire.Exchange && len(f.Payload) <= wire.ExchangeMaxPayload && len(p) >= wire.ExchangeMinSize) ||
			(f.Kind == wire.ExchangeReply && len(f.Payload) >= 9 && len(f.Payload) <= wire.ExchangeMaxPayload+9 && f.Payload[8] <= 1 && len(p) >= wire.ExchangeMinSize)) {
			return wire.Frame{}, false, false
		}
	} else if f.Kind != wire.Data && f.Kind != wire.Ping && f.Kind != wire.Pong {
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

func send(conn datagramSocket, addr netip.AddrPort, data []byte, stats *Stats) bool {
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
