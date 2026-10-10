package transport

import (
	"context"
	"encoding/binary"
	"net"
	"net/netip"
	"time"

	"customwireguard/internal/config"
	"customwireguard/internal/wire"
)

// Bounded buffering of already encrypted native WireGuard datagrams. No
// transport-level retransmission or ordering dependency is introduced.
type exchangeItem struct {
	data []byte
	at   time.Time
}
type exchangeQueue struct {
	items    []exchangeItem
	small    []exchangeItem
	stats    *Stats
	maxAge   time.Duration
	burstAge time.Duration
	capacity int
	smallRun int
}

func newExchangeQueue(cfg config.Config, stats *Stats) exchangeQueue {
	q := exchangeQueue{stats: stats, maxAge: 500 * time.Millisecond, capacity: 128}
	if cfg.ExchangeQueueMS > 0 {
		q.maxAge = time.Duration(cfg.ExchangeQueueMS) * time.Millisecond
		q.capacity = 2048
	}
	q.burstAge = time.Duration(cfg.ExchangeBurstMS) * time.Millisecond
	if cfg.ExchangeQueuePackets > 0 {
		q.capacity = cfg.ExchangeQueuePackets
	}
	return q
}

func (q *exchangeQueue) length() int { return len(q.items) + len(q.small) }
func (q *exchangeQueue) rebase(now time.Time) {
	for _, items := range [][]exchangeItem{q.items, q.small} {
		for i := range items {
			items[i].at = now
		}
	}
}
func (q *exchangeQueue) gauge() {
	q.stats.ExchangeQueuePackets.Store(uint64(q.length()))
	q.stats.ExchangeSmallQueuePackets.Store(uint64(len(q.small)))
}

func (q *exchangeQueue) expire(now time.Time) {
	limit := q.maxAge
	if limit == 0 {
		limit = 500 * time.Millisecond
	}
	for i, queue := range []*[]exchangeItem{&q.items, &q.small} {
		deadline := limit
		if i == 0 && q.burstAge > 0 {
			deadline = max(limit, q.burstAge)
		}
		for len(*queue) > 0 && now.Sub((*queue)[0].at) > deadline {
			(*queue)[0] = exchangeItem{}
			*queue = (*queue)[1:]
			q.stats.ExchangeQueueDropped.Add(1)
			q.stats.ExchangeQueueExpired.Add(1)
		}
	}
	q.gauge()
}
func (q *exchangeQueue) push(data []byte, now time.Time) {
	q.expire(now)
	capacity := q.capacity
	if capacity == 0 {
		capacity = 128
	}
	queue := &q.items
	full := q.length() >= capacity
	if q.burstAge > 0 {
		// Size is the only hint available for encrypted WireGuard packets.
		// Reserve bounded room for small packets, without claiming flow identity.
		if len(data) <= 256 {
			queue = &q.small
			full = full || len(q.small) >= 128
		} else {
			full = full || len(q.items) >= capacity-128
		}
	}
	if full {
		q.stats.ExchangeQueueDropped.Add(1)
		q.stats.ExchangeQueueFull.Add(1)
		return
	}
	*queue = append(*queue, exchangeItem{data, now})
	q.gauge()
	storeMax(&q.stats.ExchangeQueuePeak, uint64(q.length()))
}
func (q *exchangeQueue) pop(now time.Time) []byte {
	q.expire(now)
	if q.length() == 0 {
		return nil
	}
	queue := &q.items
	// At most four small packets may pass a waiting bulk packet. Keep order
	// within each class and prevent either class starving the other.
	if len(q.small) > 0 && (len(q.items) == 0 || q.smallRun < 4) {
		queue = &q.small
		q.smallRun++
		q.stats.ExchangeSmallSent.Add(1)
		storeMax(&q.stats.ExchangeSmallDelayMaxUS, uint64(max(0, now.Sub(q.small[0].at).Microseconds())))
	} else {
		q.smallRun = 0
	}
	data := (*queue)[0].data
	storeMax(&q.stats.ExchangeQueueDelayMaxUS, uint64(max(0, now.Sub((*queue)[0].at).Microseconds())))
	(*queue)[0] = exchangeItem{}
	*queue = (*queue)[1:]
	q.gauge()
	return data
}
func (q *exchangeQueue) clear() {
	q.stats.ExchangeQueueDropped.Add(uint64(q.length()))
	q.items = nil
	q.small = nil
	q.smallRun = 0
	q.gauge()
}

type exchangeFlight struct {
	at   time.Time
	size int
}

func runExchangeClient(ctx context.Context, local, remote *net.UDPConn, cfg config.Config, stats *Stats) error {
	if cfg.ExchangeMaxPPS > 0 {
		return runAdaptiveExchangeClient(ctx, local, remote, cfg, stats)
	}
	if err := cfg.Validate("client"); err != nil {
		local.Close()
		remote.Close()
		return err
	}
	key, _ := cfg.Secret()
	bootstrap, err := wire.NewExchangeCodec(key, nil, true)
	if err != nil {
		local.Close()
		remote.Close()
		return err
	}
	wgAddr := netip.MustParseAddrPort(cfg.WireGuard)
	channels, cleanup := readers(ctx, stats, local, remote)
	defer cleanup()
	upload := newExchangeQueue(cfg, stats)
	defer upload.clear()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	poll := time.NewTimer(time.Hour)
	poll.Stop()
	defer poll.Stop()
	pps := cfg.ExchangePPS
	if pps == 0 {
		pps = 100
	}
	fastInterval := time.Second / time.Duration(pps)
	var active *association
	var nextHello, nextPoll, fastUntil time.Time
	attempt := time.Now()
	id := wire.NewID()
	flights := make(map[uint64]exchangeFlight)
	defer stats.ExchangePending.Store(0)
	arm := func(now time.Time, delay time.Duration) { nextPoll = now.Add(delay); poll.Reset(delay) }
	busy := func(now time.Time) {
		fastUntil = now.Add(2 * time.Second)
		if active != nil && nextPoll.After(now.Add(fastInterval)) {
			arm(now, fastInterval)
		}
	}
	for {
		var pollC <-chan time.Time
		if active != nil {
			pollC = poll.C
		}
		select {
		case <-ctx.Done():
			return nil
		case now := <-tick.C:
			if active != nil && now.Sub(active.lastSeen) > 15*time.Second {
				active = nil
				poll.Stop()
				upload.clear()
				clear(flights)
				stats.ExchangePending.Store(0)
				id = wire.NewID()
				attempt = now
				nextHello = time.Time{}
			}
			if active != nil || now.Before(nextHello) {
				continue
			}
			if now.Sub(attempt) > 15*time.Second {
				id = wire.NewID()
				attempt = now
			}
			out, e := bootstrap.Seal(wire.Frame{Kind: wire.Hello, ID: id, Timestamp: now.Unix()}, wire.RandomBetween(256, 512))
			if e != nil {
				return e
			}
			send(remote, zeroAddr, out, stats)
			nextHello = now.Add(time.Duration(wire.RandomBetween(1700, 3000)) * time.Millisecond)
		case <-pollC:
			now := time.Now()
			for seq, flight := range flights {
				if now.Sub(flight.at) > 3*time.Second {
					delete(flights, seq)
					stats.ExchangeExpired.Add(1)
				}
			}
			if len(flights) < 512 {
				payload := upload.pop(now)
				size := wire.RandomBetween(wire.ExchangeMinSize, wire.MaxDatagram)
				out, e := active.sealSized(wire.Exchange, payload, size, now)
				if e != nil {
					return e
				}
				if send(remote, zeroAddr, out, stats) {
					flights[active.send] = exchangeFlight{now, size}
					stats.ExchangeRequests.Add(1)
					if len(payload) > 0 {
						stats.Sent.Add(1)
					}
				}
			} else {
				stats.ExchangeWindowFull.Add(1)
			}
			stats.ExchangePending.Store(uint64(len(flights)))
			interval := 250 * time.Millisecond
			if now.Before(fastUntil) || upload.length() > 0 {
				interval = fastInterval
			}
			arm(time.Now(), interval*time.Duration(wire.RandomBetween(80, 120))/100)
		case p := <-channels[0]:
			if p.addr != wgAddr {
				stats.Rejected.Add(1)
				continue
			}
			if len(p.data) == 0 || len(p.data) > wire.ExchangeMaxPayload {
				stats.Oversize.Add(1)
				continue
			}
			now := time.Now()
			upload.push(p.data, p.at)
			busy(now)
		case p := <-channels[1]:
			now := time.Now()
			if active == nil {
				f, e := bootstrap.Open(p.data, now)
				if e != nil || f.Kind != wire.Welcome || f.ID != id || f.Sequence != 0 || len(f.Payload) != 32 || len(p.data) < 256 || len(p.data) > 512 {
					stats.Rejected.Add(1)
					continue
				}
				active, e = newAssociationMode(key, id, f.Payload, true, now, true)
				if e != nil {
					return e
				}
				stats.Established.Add(1)
				fastUntil = now.Add(2 * time.Second)
				arm(now, 0)
				continue
			}
			f, ok, _ := active.open(p.data, now)
			if !ok || f.Kind != wire.ExchangeReply {
				stats.Rejected.Add(1)
				continue
			}
			seq := binary.BigEndian.Uint64(f.Payload[:8])
			flight, exists := flights[seq]
			if !exists || len(p.data) != flight.size {
				stats.Rejected.Add(1)
				continue
			}
			delete(flights, seq)
			stats.ExchangePending.Store(uint64(len(flights)))
			stats.ExchangeReplies.Add(1)
			if len(f.Payload) > 9 {
				if send(local, wgAddr, f.Payload[9:], stats) {
					stats.Received.Add(1)
				}
				busy(now)
			} else {
				stats.ExchangeEmpty.Add(1)
			}
			if f.Payload[8] != 0 {
				busy(now)
			}
		}
	}
}
