package transport

import (
	"testing"
	"time"

	"customwireguard/internal/config"
)

func TestAdaptiveControllerGrowthBackoffAndIdle(t *testing.T) {
	now := time.Unix(1000, 0)
	s := new(Stats)
	c := newExchangeControl(200, 6000, now, s)
	for i := 0; i < 6; i++ {
		for n := 0; n < 32; n++ {
			c.reply(now, 90*time.Millisecond, true, true)
		}
		now = now.Add(100 * time.Millisecond)
		c.update(now, false)
	}
	if c.rate != 6000 || c.window() < 540 {
		t.Fatal("did not fill long-RTT pipeline", c.rate, c.window())
	}
	for n := 0; n < 32; n++ {
		c.reply(now, 130*time.Millisecond, true, true)
	}
	now = now.Add(150 * time.Millisecond)
	c.update(now, false)
	if c.rate >= 6000 || s.ExchangeBackoffs.Load() == 0 {
		t.Fatal("queue delay did not cause backoff")
	}
	old := c.rate
	for n := 0; n < 32; n++ {
		c.reply(now, 90*time.Millisecond, true, true)
	}
	now = now.Add(150 * time.Millisecond)
	c.update(now, false)
	if c.rate > old {
		t.Fatal("grew during recovery cooldown")
	}
	now = now.Add(time.Second)
	c.lost = 10
	for n := 0; n < 90; n++ {
		c.reply(now, 90*time.Millisecond, true, false)
	}
	c.update(now, false)
	if c.rate >= old {
		t.Fatal("loss did not cause backoff")
	}
	for i := 0; i < 60; i++ {
		for n := 0; n < 32; n++ {
			c.reply(now, 90*time.Millisecond, false, false)
		}
		now = now.Add(time.Second)
		c.update(now, false)
	}
	if c.rate != 200 {
		t.Fatal("unused capacity did not return to baseline", c.rate)
	}
	if c.timeout() < 300*time.Millisecond || c.timeout() > 2*time.Second {
		t.Fatal("unbounded timeout")
	}
}

func TestAdaptiveDoesNotGrowWithoutReplies(t *testing.T) {
	now := time.Now()
	c := newExchangeControl(200, 12000, now, new(Stats))
	for i := 0; i < 100; i++ {
		now = now.Add(time.Second)
		c.update(now, true)
	}
	if c.rate != 200 {
		t.Fatal("unacknowledged traffic increased sending rate")
	}
}

func TestAdaptiveReductionWaitsForNewFlightFeedback(t *testing.T) {
	start := time.Unix(1000, 0)
	for _, cause := range []string{"delay", "loss"} {
		t.Run(cause, func(t *testing.T) {
			s := new(Stats)
			c := newExchangeControl(200, 9000, start, s)
			c.rate, c.srtt, c.minRTT = 9000, 90*time.Millisecond, 90*time.Millisecond
			cut := start.Add(200 * time.Millisecond)
			for i := 0; i < 100; i++ {
				c.reply(cut, 130*time.Millisecond, true, true)
			}
			c.update(cut, true)
			if c.rate != 6300 || s.ExchangeDelayBackoffs.Load() != 1 {
				t.Fatal("first delay did not reduce rate", c.rate)
			}
			// These losses and slow replies belong to the pre-reduction flight.
			// They may outlive the growth cooldown, so a timer alone is not enough.
			f := new(exchangeFlights)
			for i := uint64(1); i <= 46; i++ {
				f.add(i, cut.Add(-time.Millisecond), 1400)
			}
			now := cut.Add(500 * time.Millisecond)
			total, current := f.expire(now, 300*time.Millisecond, c.delayReduction)
			if total != 46 || current != 0 || f.count != 0 {
				t.Fatal("old flight was not fully retired", total, current, f.count)
			}
			c.lost += current
			for i := 0; i < 100; i++ {
				c.reply(now, 501*time.Millisecond, true, true)
			}
			c.update(now, true)
			if c.rate != 6300 || s.ExchangeBackoffs.Load() != 1 || s.ExchangePriorFlightReplies.Load() != 100 {
				t.Fatal("old feedback changed the new rate", c.rate, s.ExchangeBackoffs.Load())
			}
			// Continued congestion among requests sent at the reduced rate must
			// still cut the rate; there is no blind backoff cooldown.
			now = now.Add(time.Second)
			if cause == "loss" {
				for i := uint64(47); i <= 66; i++ {
					f.add(i, cut, 1400) // Equality belongs to the new flight.
				}
				total, current = f.expire(now, 300*time.Millisecond, c.delayReduction)
				if total != 20 || current != total {
					t.Fatal("fresh loss suppressed", total, current)
				}
				c.lost += current
			} else {
				for i := 0; i < 100; i++ {
					c.reply(now, 130*time.Millisecond, true, true)
				}
			}
			c.update(now, true)
			if c.rate != 4410 || s.ExchangeBackoffs.Load() != 2 {
				t.Fatal("fresh congestion did not reduce rate", c.rate)
			}
			if cause == "loss" && s.ExchangeLossBackoffs.Load() != 1 {
				t.Fatal("loss reason missing")
			}
			if cause == "delay" && s.ExchangeDelayBackoffs.Load() != 2 {
				t.Fatal("delay reason missing")
			}
		})
	}
}

func TestAdaptiveMixedFlightExpiryKeepsAccountingAndBounds(t *testing.T) {
	now := time.Unix(1000, 0)
	cut := now.Add(time.Millisecond)
	f := new(exchangeFlights)
	f.add(1, now, 1400)
	f.add(2, cut, 1400)
	f.add(3, cut.Add(time.Millisecond), 1400)
	f.add(4, now.Add(time.Second), 1400)
	if _, ok := f.take(3, 1400); !ok {
		t.Fatal("reordered reply rejected")
	}
	if total, current := f.expire(now.Add(time.Second), 300*time.Millisecond, cut); total != 2 || current != 1 || f.count != 1 {
		t.Fatal("mixed expiry accounting", total, current, f.count)
	}
	if total, current := f.expire(now.Add(2*time.Second), 300*time.Millisecond, cut); total != 1 || current != 1 || f.count != 0 {
		t.Fatal("blackout retained flights", total, current, f.count)
	}
}

func TestAdaptiveLossOnlyRetainsConservativeTimeoutResponse(t *testing.T) {
	now := time.Unix(1000, 0)
	c := newExchangeControl(200, 9000, now, new(Stats))
	c.rate, c.srtt, c.minRTT = 9000, 90*time.Millisecond, 90*time.Millisecond
	f := new(exchangeFlights)
	for i := uint64(1); i <= 20; i++ {
		f.add(i, now, 1400)
	}
	c.lost = 20
	c.update(now.Add(100*time.Millisecond), true)
	if c.rate != 6300 || !c.delayReduction.IsZero() {
		t.Fatal("loss backoff masked later losses")
	}
	// A later batch of losses, with no preceding delay signal, must retain the
	// previous conservative response even if sent before the first loss cut.
	total, current := f.expire(now.Add(time.Second), c.timeout(), c.delayReduction)
	if total != 20 || current != 20 {
		t.Fatal("loss-only feedback suppressed", total, current)
	}
	c.lost += current
	c.update(now.Add(time.Second), true)
	if c.rate != 4410 {
		t.Fatal("persistent loss did not back off", c.rate)
	}
}

func TestAdaptiveLossDenominatorKeepsSuccessfulOlderFlight(t *testing.T) {
	start := time.Unix(1000, 0)
	stats := new(Stats)
	c := newExchangeControl(200, 8000, start, stats)
	c.rate, c.srtt, c.minRTT = 8000, 100*time.Millisecond, 100*time.Millisecond
	cut := start.Add(200 * time.Millisecond)
	for i := 0; i < 776; i++ {
		c.reply(cut, 100*time.Millisecond, true, true)
	}
	c.lost = 24
	c.update(cut, true) // 3% measured loss still reduces immediately.
	if c.rate != 5600 || stats.ExchangeLossBackoffs.Load() != 1 {
		t.Fatal("real loss did not reduce rate", c.rate)
	}
	// An old flight produces 900 authenticated successes and 18 timeouts.
	// It cannot justify growth/delay decisions at the new rate, but counting
	// only its timeouts would fabricate 100% loss instead of 1.96%.
	now := cut.Add(100 * time.Millisecond)
	for i := 0; i < 900; i++ {
		c.reply(now, 101*time.Millisecond, true, true)
	}
	c.lost = 18
	c.update(now.Add(time.Millisecond), true)
	if c.rate != 5600 || stats.ExchangeLossBackoffs.Load() != 1 || stats.ExchangePriorFlightReplies.Load() != 900 {
		t.Fatal("successful old replies disappeared from loss denominator", c.rate, stats.Snapshot())
	}
	// Ongoing congestion is not hidden behind a blind cooldown.
	now = now.Add(150 * time.Millisecond)
	for i := 0; i < 100; i++ {
		c.reply(now, 100*time.Millisecond, true, true)
	}
	c.lost = 20
	c.update(now, true)
	if c.rate != 3920 || stats.ExchangeLossBackoffs.Load() != 2 {
		t.Fatal("fresh persistent loss failed to reduce", c.rate)
	}
}

func TestAdaptiveResumesOnlyRecentDeliveredCapacity(t *testing.T) {
	now := time.Unix(1000, 0)
	s := new(Stats)
	c := newExchangeControl(200, 9000, now, s)
	// Fill each 100 ms interval with actual replies at its current rate.
	for i := 0; i < 8; i++ {
		now = now.Add(100 * time.Millisecond)
		for n := 0; n < c.rate/10; n++ {
			c.reply(now, 90*time.Millisecond, true, true)
		}
		c.update(now, true)
	}
	if c.provenRate != 9000 {
		t.Fatal("capacity not learned", c.provenRate)
	}
	// An application pause lets the rate decay, not the recent delivery memory.
	for i := 0; i < 30; i++ {
		now = now.Add(100 * time.Millisecond)
		for n := 0; n < 20; n++ {
			c.reply(now, 90*time.Millisecond, false, false)
		}
		c.update(now, false)
	}
	if c.rate >= 9000 {
		t.Fatal("idle traffic did not decay")
	}
	c.resume(now)
	if c.rate != 9000 || s.ExchangeResumes.Load() != 1 {
		t.Fatal("fresh burst did not resume", c.rate)
	}
	for _, condition := range []string{"expired", "stale-reply", "loss", "delay", "cooldown"} {
		copy := *c
		copy.rate = 200
		switch condition {
		case "expired":
			copy.provenUntil = now
		case "stale-reply":
			copy.lastReply = now.Add(-time.Second)
		case "loss":
			copy.lost = 1
		case "delay":
			copy.srtt = 130 * time.Millisecond
		case "cooldown":
			copy.cooldown = now.Add(time.Second)
		}
		copy.resume(now)
		if copy.rate != 200 {
			t.Fatal("unsafe resume", condition)
		}
	}
	c.lost = 50
	now = now.Add(100 * time.Millisecond)
	for n := 0; n < 100; n++ {
		c.reply(now, 90*time.Millisecond, true, true)
	}
	c.update(now, true)
	if c.provenRate != 0 {
		t.Fatal("backoff retained old capacity")
	}
	rate := c.rate
	c.resume(now.Add(time.Second))
	if c.rate != rate {
		t.Fatal("old capacity bypassed congestion backoff")
	}
	now = now.Add(time.Second)
	for n := 0; n < 1000; n++ {
		c.reply(now, 90*time.Millisecond, true, true)
	}
	c.update(now, true)
	if c.rate > rate+max(100, rate/10) {
		t.Fatal("recovery repeated cold-start jump")
	}
}

func TestAdaptiveGrowthDoesNotInventProvenCapacity(t *testing.T) {
	now := time.Unix(1000, 0)
	c := newExchangeControl(200, 9000, now, new(Stats))
	for i := 0; i < 8; i++ {
		now = now.Add(100 * time.Millisecond)
		for n := 0; n < 8; n++ {
			c.reply(now, 90*time.Millisecond, true, true)
		}
		c.update(now, true)
	}
	if c.provenRate != 0 {
		t.Fatal("sparse acknowledgments proved a high rate")
	}
}

func TestAdaptivePacerRateAndStall(t *testing.T) {
	for _, rate := range []int{4, 200, 1000, 6000, 7500, 9000, 12000} {
		p := exchangePacer{}
		now := time.Unix(1000, 0)
		sent := 0
		for i := 0; i < 10000; i++ {
			sent += p.ready(now, rate)
			now = now.Add(100 * time.Microsecond)
		}
		if sent < rate-2 || sent > rate+2 {
			t.Fatalf("rate%d sent%d", rate, sent)
		}
		if n := p.ready(now.Add(time.Hour), rate); n > min(32, max(1, (rate+499)/500)) {
			t.Fatal("stall catch-up burst", n)
		}
	}
	p := exchangePacer{}
	now := time.Now()
	p.ready(now, 4)
	if n := p.ready(now.Add(250*time.Millisecond), 12000); n > 1 {
		t.Fatal("idle transition minted burst", n)
	}
}

func TestAdaptiveFlightsBoundExpiryAndReorder(t *testing.T) {
	f := new(exchangeFlights)
	now := time.Now()
	for i := uint64(1); i <= 16384; i++ {
		if !f.available(i) {
			t.Fatal("early cap")
		}
		f.add(i, now, 1400+int(i%40))
	}
	if f.available(16385) {
		t.Fatal("missing hard bound")
	}
	if _, ok := f.take(2, 1403); ok {
		t.Fatal("wrong-size reply accepted")
	}
	if _, ok := f.take(2, 1402); !ok {
		t.Fatal("reordered response rejected")
	}
	if _, ok := f.take(2, 1402); ok {
		t.Fatal("duplicate response accepted")
	}
	if n, current := f.expire(now.Add(time.Second), 300*time.Millisecond, time.Time{}); n != 16383 || current != n || f.count != 0 {
		t.Fatal("expiry count", n, current, f.count)
	}
	seq := uint64(32770)
	f.add(seq, now.Add(time.Second), 1402)
	if _, ok := f.take(2, 1402); ok {
		t.Fatal("old reply matched reused slot")
	}
	if _, ok := f.take(seq, 1402); !ok {
		t.Fatal("new reply rejected")
	}
}

func TestAdaptiveQueueAgeCounters(t *testing.T) {
	s := new(Stats)
	q := newExchangeQueue(config.Config{ExchangeQueueMS: 60}, s)
	now := time.Now()
	q.push([]byte{1}, now)
	if got := q.pop(now.Add(61 * time.Millisecond)); got != nil || s.ExchangeQueueExpired.Load() != 1 {
		t.Fatal("stale datagram forwarded")
	}
	for i := 0; i < 2049; i++ {
		q.push([]byte{2}, now)
	}
	if len(q.items) != 2048 || s.ExchangeQueueFull.Load() != 1 {
		t.Fatal("queue unbounded")
	}
	q.clear()
	if s.ExchangeQueuePackets.Load() != 0 {
		t.Fatal("stale occupancy")
	}
}
