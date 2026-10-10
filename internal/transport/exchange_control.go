package transport

import (
	"math"
	"time"
)

// The controller uses only authenticated request/reply timing and queue demand.
// A reply is sent immediately by the server, even when its native data queue is
// empty, so outer RTT does not include the native queue's residence time.
type exchangeControl struct {
	base, ceiling, rate    int
	stats                  *Stats
	srtt, minRTT, epochMin time.Duration
	epoch, cooldown        time.Time
	acks, useful, lost     int
	backlog                bool
	quiet                  int
	provenRate             int
	provenUntil            time.Time
	lastReply              time.Time
	recovering             bool
	lastReduction          time.Time
	delayReduction         time.Time
}

// Resume only capacity recently sustained by authenticated deliveries. This
// avoids repeating slow start for each application burst, while retaining the
// normal pacer and congestion response. No traffic is sent solely to keep this
// memory alive, and a fresh association starts with no remembered capacity.
func (c *exchangeControl) resume(now time.Time) {
	if c.provenRate > c.rate && now.Before(c.provenUntil) && !now.Before(c.cooldown) && c.lost == 0 && !c.lastReply.IsZero() && now.Sub(c.lastReply) <= max(500*time.Millisecond, 3*c.srtt) && c.srtt <= c.minRTT+25*time.Millisecond {
		c.rate = min(c.ceiling, c.provenRate)
		c.quiet = 0
		c.stats.ExchangeRateIncreases.Add(1)
		c.stats.ExchangeResumes.Add(1)
	}
}

func newExchangeControl(base, ceiling int, now time.Time, stats *Stats) *exchangeControl {
	if base == 0 {
		base = 100
	}
	return &exchangeControl{base: base, ceiling: ceiling, rate: base, epoch: now, stats: stats}
}

func (c *exchangeControl) reply(now time.Time, rtt time.Duration, useful, more bool) {
	if rtt <= 0 {
		return
	}
	c.lastReply = now
	if c.srtt == 0 {
		c.srtt = rtt
	} else {
		c.srtt = (7*c.srtt + rtt) / 8
	}
	if c.minRTT == 0 || rtt < c.minRTT {
		c.minRTT = rtt
	}
	c.stats.ExchangeRTTUS.Store(uint64(c.srtt.Microseconds()))
	// Feedback for requests sent before the latest reduction describes the
	// previous sending rate. Keep its RTT and delivery accounting, but do not
	// use it to reduce again or justify growth at the new rate. Payloads are
	// delivered by the caller regardless of this controller-only decision.
	if now.Add(-rtt).Before(c.lastReduction) {
		c.stats.ExchangePriorFlightReplies.Add(1)
		return
	}
	if c.epochMin == 0 || rtt < c.epochMin {
		c.epochMin = rtt
	}
	c.acks++
	if useful {
		c.useful++
	}
	c.backlog = c.backlog || more
}

func (c *exchangeControl) update(now time.Time, uploadPending bool) {
	period := max(100*time.Millisecond, c.srtt)
	if now.Sub(c.epoch) < period {
		return
	}
	old := c.rate
	elapsed := now.Sub(c.epoch)
	total := c.acks + c.lost
	loss := total >= 16 && c.lost*100 > total*2
	delay := c.acks >= 16 && c.epochMin > c.minRTT+25*time.Millisecond
	if loss || delay {
		c.rate = max(c.base, c.rate*7/10)
		c.lastReduction = now
		// Old capacity must never bypass a backoff after the path changes.
		c.provenRate = 0
		c.provenUntil = time.Time{}
		c.recovering = true
		c.cooldown = now.Add(max(200*time.Millisecond, 2*c.srtt))
		c.quiet = 0
		c.stats.ExchangeBackoffs.Add(1)
		if loss {
			c.stats.ExchangeLossBackoffs.Add(1)
		}
		if delay {
			// A delay-triggered reduction already reacted to the queued flight.
			// Its later timeouts must not count the same disturbance again.
			// Loss-only reductions retain the conservative timeout response:
			// ignoring their old losses can sustain an overload for too long.
			c.delayReduction = now
			c.stats.ExchangeDelayBackoffs.Add(1)
		}
	} else if !now.Before(c.cooldown) && c.acks >= 8 && (uploadPending || c.backlog || c.useful*4 >= c.acks*3) {
		// A growth decision alone is not proof the higher rate was delivered.
		if c.lost == 0 && float64(c.acks) >= float64(c.rate)*elapsed.Seconds()*.8 && c.rate >= c.provenRate {
			c.provenRate = c.rate
			c.provenUntil = now.Add(30 * time.Second)
		}
		// A cold burst cannot survive many RTT-sized doubling intervals within
		// the bounded bulk deadline. Grow by at most four per interval, only
		// with authenticated delivery and demand, retaining delay/loss backoff.
		growth := c.rate * 4
		if c.recovering {
			// Once this association found congestion, probe conservatively
			// instead of repeating cold-start jumps into the same bottleneck.
			growth = c.rate + max(100, c.rate/10)
		}
		c.rate = min(c.ceiling, growth)
		c.quiet = 0
	} else if !uploadPending && !c.backlog && c.useful*3 < c.acks {
		c.quiet++
		if c.quiet >= 3 {
			c.rate = max(c.base, c.rate*3/4)
			c.quiet = 0
		}
	} else {
		c.quiet = 0
	}
	if c.rate > old {
		c.stats.ExchangeRateIncreases.Add(1)
	}
	c.epoch = now
	c.epochMin = 0
	c.acks = 0
	c.useful = 0
	c.lost = 0
	c.backlog = false
}

func (c *exchangeControl) timeout() time.Duration {
	return max(300*time.Millisecond, min(2*time.Second, 4*c.srtt))
}
func (c *exchangeControl) window() int {
	rtt := c.srtt
	if rtt == 0 {
		rtt = 100 * time.Millisecond
	}
	return min(16384, max(64, int(math.Ceil(float64(c.rate)*(2*rtt+50*time.Millisecond).Seconds()))+32))
}

// Time-based credits coalesce high-rate work into <=2 ms batches (max32),
// without a high-resolution timer per datagram or catch-up bursts after stalls.
type exchangePacer struct {
	last   time.Time
	tokens float64
	rate   int
}

func (p *exchangePacer) ready(now time.Time, rate int) int {
	if p.last.IsZero() {
		p.last = now
		p.tokens = 1
	}
	// Accrue at the previous rate; changing from idle must not mint a burst.
	if p.rate > 0 {
		p.tokens += max(0, now.Sub(p.last).Seconds()) * float64(p.rate)
	}
	p.last = now
	p.rate = rate
	limit := min(32, max(1, int(math.Ceil(float64(rate)*.002))))
	p.tokens = min(float64(limit), p.tokens)
	// Avoid losing a whole credit to floating-point roundoff at exact timer
	// boundaries (for example ten 100 us steps at 1000 requests/s).
	n := int(p.tokens + 1e-9)
	p.tokens = max(0, p.tokens-float64(n))
	return n
}
func (p *exchangePacer) wait() time.Duration {
	return max(time.Millisecond, time.Duration((1-p.tokens)/float64(p.rate)*float64(time.Second)))
}

type adaptiveFlight struct {
	seq  uint64
	at   time.Time
	size int
}

// Fixed storage bounds memory even with complete packet loss. In-order expiry
// visits each slot once, rather than scanning every outstanding request on every
// send. Exact sequence checks prevent an old reply matching a reused slot.
type exchangeFlights struct {
	slots      [32768]adaptiveFlight
	head, tail uint64
	count      int
}

func (f *exchangeFlights) available(seq uint64) bool {
	return f.count < 16384 && f.slots[seq%uint64(len(f.slots))].seq == 0
}
func (f *exchangeFlights) add(seq uint64, now time.Time, size int) {
	if f.head == 0 {
		f.head = seq
	}
	f.tail = seq
	f.slots[seq%uint64(len(f.slots))] = adaptiveFlight{seq, now, size}
	f.count++
}
func (f *exchangeFlights) take(seq uint64, size int) (adaptiveFlight, bool) {
	slot := &f.slots[seq%uint64(len(f.slots))]
	if seq == 0 || slot.seq != seq || slot.size != size {
		return adaptiveFlight{}, false
	}
	value := *slot
	*slot = adaptiveFlight{}
	f.count--
	return value, true
}
func (f *exchangeFlights) expire(now time.Time, limit time.Duration, cutoff time.Time) (total, current int) {
	for f.head != 0 && f.head <= f.tail {
		slot := &f.slots[f.head%uint64(len(f.slots))]
		if slot.seq == f.head {
			if now.Sub(slot.at) <= limit {
				break
			}
			if !slot.at.Before(cutoff) {
				current++
			}
			*slot = adaptiveFlight{}
			f.count--
			total++
		}
		if f.head == f.tail {
			f.head = 0
			break
		}
		f.head++
	}
	return total, current
}
