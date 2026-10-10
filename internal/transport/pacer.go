package transport

import (
	"net/netip"
	"time"

	"customwireguard/internal/config"
)

type pacedPacket struct {
	packet
	enqueued time.Time
}

// packetPacer is owned by the transport event loop. It spaces encrypted data
// datagrams without blocking incoming traffic or accumulating burst credit.
// This is a fixed outbound rate ceiling, not adaptive congestion control.
type packetPacer struct {
	rate     int64
	maxDelay time.Duration
	next     time.Time
	queue    []pacedPacket
	bytes    int
	timer    *time.Timer
	stats    *Stats
	write    func(packet) bool
}

func newPacketPacer(cfg config.Config, conn datagramSocket, stats *Stats) *packetPacer {
	ms := cfg.PaceQueueMS
	if ms == 0 {
		ms = 150
	}
	p := &packetPacer{rate: int64(cfg.PaceKbps) * 1000, maxDelay: time.Duration(ms) * time.Millisecond, stats: stats}
	p.write = func(q packet) bool { return send(conn, q.addr, q.data, stats) }
	if p.rate != 0 {
		p.timer = time.NewTimer(time.Hour)
		p.timer.Stop()
	}
	return p
}

func (p *packetPacer) cost(bytes int) time.Duration {
	// Conservatively include UDP + IPv6 base headers on either IP family.
	return time.Duration((int64(bytes+48)*8*int64(time.Second) + p.rate - 1) / p.rate)
}

func (p *packetPacer) emit(q packet, now time.Time) {
	if p.write(q) {
		p.stats.Sent.Add(1)
	}
	if finished := time.Now(); finished.After(now) {
		now = finished
	}
	if p.rate != 0 {
		p.next = now.Add(p.cost(len(q.data)))
	}
}

func (p *packetPacer) push(data []byte, addr netip.AddrPort, now time.Time) {
	q := packet{data: data, addr: addr}
	if p.rate == 0 || (len(p.queue) == 0 && !now.Before(p.next)) {
		p.emit(q, now)
		return
	}
	wait := p.next.Sub(now)
	if wait < 0 {
		wait = 0
	}
	for _, item := range p.queue {
		wait += p.cost(len(item.data))
	}
	if len(p.queue) >= 256 || wait > p.maxDelay {
		p.stats.PacingDropped.Add(1)
		return
	}
	p.queue = append(p.queue, pacedPacket{packet: q, enqueued: now})
	p.bytes += len(data)
	p.stats.PacingQueued.Add(1)
	p.stats.PacingQueueBytes.Store(uint64(p.bytes))
	if uint64(p.bytes) > p.stats.PacingPeakBytes.Load() {
		p.stats.PacingPeakBytes.Store(uint64(p.bytes))
	}
	if len(p.queue) == 1 {
		p.arm(now)
	}
}

func (p *packetPacer) arm(now time.Time) {
	if len(p.queue) == 0 {
		p.timer.Stop()
		return
	}
	delay := p.next.Sub(now)
	if delay < 0 {
		delay = 0
	}
	p.timer.Reset(delay)
}

func (p *packetPacer) ready() <-chan time.Time {
	if p.timer == nil || len(p.queue) == 0 {
		return nil
	}
	return p.timer.C
}

func (p *packetPacer) drain(now time.Time) {
	if now.Before(p.next) {
		p.arm(now)
		return
	}
	for len(p.queue) != 0 {
		q := p.queue[0]
		p.queue[0] = pacedPacket{}
		p.queue = p.queue[1:]
		p.bytes -= len(q.data)
		p.stats.PacingQueueBytes.Store(uint64(p.bytes))
		if now.Sub(q.enqueued) > p.maxDelay {
			p.stats.PacingDropped.Add(1)
			continue
		}
		p.stats.PacingWaitUS.Add(uint64(now.Sub(q.enqueued).Microseconds()))
		p.emit(q.packet, now)
		break // Never catch up with a burst after a scheduler stall.
	}
	p.arm(now)
}

func (p *packetPacer) reset() {
	if p.timer != nil {
		p.timer.Stop()
	}
	p.stats.PacingDropped.Add(uint64(len(p.queue)))
	p.queue = nil
	p.bytes = 0
	p.next = time.Time{}
	p.stats.PacingQueueBytes.Store(0)
}

func (p *packetPacer) rebind(addr netip.AddrPort) {
	for i := range p.queue {
		p.queue[i].addr = addr
	}
}
