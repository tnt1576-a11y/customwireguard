package transport

import "time"

const exchangeReorderSlots = 128

type reorderItem struct {
	seq  uint64
	at   time.Time
	data []byte
}

// Restore short outer-packet inversions before native WireGuard delivery. This
// is not reliable transport: a hole is skipped after a bounded delay, and an
// authenticated late packet is still delivered. Empty exchanges advance the
// sequence too. Authentication/replay checks and congestion feedback precede it.
// Owned by the transport event loop; fixed storage bounds loss/reordering memory.
type exchangeReorder struct {
	delay time.Duration
	next  uint64
	count int
	slots [exchangeReorderSlots]reorderItem
	timer *time.Timer
	stats *Stats
	emit  func([]byte)
}

func newExchangeReorder(ms int, stats *Stats, emit func([]byte)) *exchangeReorder {
	r := &exchangeReorder{delay: time.Duration(ms) * time.Millisecond, next: 1, stats: stats, emit: emit}
	if ms > 0 {
		r.timer = time.NewTimer(time.Hour)
		r.timer.Stop()
	}
	return r
}

func (r *exchangeReorder) ready() <-chan time.Time {
	if r.count == 0 || r.timer == nil {
		return nil
	}
	return r.timer.C
}

func (r *exchangeReorder) reset() {
	if r.timer != nil {
		r.timer.Stop()
	}
	r.slots = [exchangeReorderSlots]reorderItem{}
	r.count, r.next = 0, 1
}

func (r *exchangeReorder) deliver(p reorderItem, now time.Time) {
	if !p.at.IsZero() {
		storeMax(&r.stats.ExchangeReorderWaitMaxUS, uint64(max(0, now.Sub(p.at).Microseconds())))
	}
	if len(p.data) > 0 {
		r.emit(p.data)
	}
}

func (r *exchangeReorder) drain(now time.Time) {
	for r.count > 0 {
		p := &r.slots[r.next%exchangeReorderSlots]
		if p.seq == 0 || p.seq != r.next {
			break
		}
		r.deliver(*p, now)
		*p = reorderItem{}
		r.count--
		r.next++
	}
}

func (r *exchangeReorder) first() (uint64, time.Time) {
	var lowest uint64
	var oldest time.Time
	for _, p := range r.slots {
		if p.seq == 0 {
			continue
		}
		if lowest == 0 || p.seq < lowest {
			lowest = p.seq
		}
		if oldest.IsZero() || p.at.Before(oldest) {
			oldest = p.at
		}
	}
	return lowest, oldest
}

func (r *exchangeReorder) flush(now time.Time, force bool) {
	for r.count > 0 {
		lowest, oldest := r.first()
		if !force && now.Before(oldest.Add(r.delay)) {
			break
		}
		r.next = lowest // skip a hole, never wait for retransmission
		r.drain(now)
	}
}

func (r *exchangeReorder) arm(now time.Time) {
	if r.timer == nil {
		return
	}
	if r.count == 0 {
		r.timer.Stop()
		return
	}
	_, oldest := r.first()
	r.timer.Reset(max(0, oldest.Add(r.delay).Sub(now)))
}

func (r *exchangeReorder) wake(now time.Time) { r.flush(now, false); r.arm(now) }

func (r *exchangeReorder) push(seq uint64, data []byte, now time.Time) {
	if seq == 0 {
		return
	}
	if r.delay == 0 {
		r.deliver(reorderItem{data: data}, now)
		return
	}
	r.flush(now, false)
	if seq < r.next {
		r.stats.ExchangeReorderLate.Add(1)
		r.deliver(reorderItem{data: data}, now)
	} else {
		if seq-r.next >= exchangeReorderSlots {
			r.stats.ExchangeReorderForced.Add(1)
			r.flush(now, true)
			r.next = seq
		}
		if seq == r.next {
			r.deliver(reorderItem{data: data}, now)
			r.next++
			r.drain(now)
		} else {
			p := &r.slots[seq%exchangeReorderSlots]
			if p.seq == 0 {
				*p = reorderItem{seq: seq, at: now, data: data}
				r.count++
				r.stats.ExchangeReorderHeld.Add(1)
			}
		}
	}
	r.arm(now)
}
