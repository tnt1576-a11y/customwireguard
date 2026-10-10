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
			c.reply(90*time.Millisecond, true, true)
		}
		now = now.Add(100 * time.Millisecond)
		c.update(now, false)
	}
	if c.rate != 6000 || c.window() < 540 {
		t.Fatal("did not fill long-RTT pipeline", c.rate, c.window())
	}
	for n := 0; n < 32; n++ {
		c.reply(130*time.Millisecond, true, true)
	}
	now = now.Add(150 * time.Millisecond)
	c.update(now, false)
	if c.rate >= 6000 || s.ExchangeBackoffs.Load() == 0 {
		t.Fatal("queue delay did not cause backoff")
	}
	old := c.rate
	for n := 0; n < 32; n++ {
		c.reply(90*time.Millisecond, true, true)
	}
	now = now.Add(150 * time.Millisecond)
	c.update(now, false)
	if c.rate > old {
		t.Fatal("grew during recovery cooldown")
	}
	now = now.Add(time.Second)
	c.lost = 10
	for n := 0; n < 90; n++ {
		c.reply(90*time.Millisecond, true, false)
	}
	c.update(now, false)
	if c.rate >= old {
		t.Fatal("loss did not cause backoff")
	}
	for i := 0; i < 60; i++ {
		for n := 0; n < 32; n++ {
			c.reply(90*time.Millisecond, false, false)
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

func TestAdaptivePacerRateAndStall(t *testing.T) {
	for _, rate := range []int{4, 200, 1000, 6000, 12000} {
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
		if n := p.ready(now.Add(time.Hour), rate); n > 32 {
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
	if n := f.expire(now.Add(time.Second), 300*time.Millisecond); n != 16383 || f.count != 0 {
		t.Fatal("expiry count", n, f.count)
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
