package transport

import (
	"encoding/binary"
	"time"

	"customwireguard/internal/wire"
)

// One delayed backup copy may use an otherwise empty exchange. Original data
// always wins. Repair does not schedule exchanges or raise the rate ceiling;
// useful replies can still influence the existing adaptive demand controller.
// No acknowledgments or blocking retransmission are introduced. WireGuard rejects
// duplicate transport counters; handshake packets are never copied.
type exchangeRepair struct {
	enabled     bool
	items       [256]exchangeItem
	head, count int
	stats       *Stats
}

func (r *exchangeRepair) expire(now time.Time) {
	for r.count > 0 && now.Sub(r.items[r.head].at) >= 30*time.Millisecond {
		r.items[r.head] = exchangeItem{}
		r.head = (r.head + 1) % len(r.items)
		r.count--
		r.stats.ExchangeRepairExpired.Add(1)
	}
}

func (r *exchangeRepair) remember(data []byte, now time.Time) {
	if !r.enabled || len(data) < 32 || len(data) > wire.ExchangeMaxPayload || binary.LittleEndian.Uint32(data[:4]) != 4 {
		return
	}
	r.expire(now)
	if r.count == len(r.items) {
		r.items[r.head] = exchangeItem{}
		r.head = (r.head + 1) % len(r.items)
		r.count--
		r.stats.ExchangeRepairExpired.Add(1)
	}
	r.items[(r.head+r.count)%len(r.items)] = exchangeItem{data: data, at: now}
	r.count++
}

func (r *exchangeRepair) pop(now time.Time) []byte {
	if !r.enabled {
		return nil
	}
	r.expire(now)
	if r.count == 0 || now.Sub(r.items[r.head].at) < 5*time.Millisecond {
		return nil
	}
	data := r.items[r.head].data
	r.items[r.head] = exchangeItem{}
	r.head = (r.head + 1) % len(r.items)
	r.count--
	return data
}

func (r *exchangeRepair) reset() {
	r.items = [256]exchangeItem{}
	r.head, r.count = 0, 0
}
