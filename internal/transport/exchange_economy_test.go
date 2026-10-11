package transport

import (
	"testing"
	"time"
)

func TestEconomyReducesEmptyPollingButKeepsRepairHeadroom(t *testing.T) {
	now := time.Unix(1000, 0)
	c := newExchangeControl(200, 8000, now, new(Stats))
	c.economy, c.rate = true, 8000
	// 1000 original uploads/s and 1000 replies/s, the latter allowed to
	// include repair. Most exchanges are empty at the old 8000/s rate.
	for epoch := 0; epoch < 12; epoch++ {
		now = now.Add(100 * time.Millisecond)
		count := c.rate / 10
		for n := 0; n < count; n++ {
			c.reply(now, 90*time.Millisecond, n < 100, n == 0)
		}
		c.uploaded = 100
		c.update(now, false)
	}
	if c.rate < 1500 || c.rate > 2250 {
		t.Fatal("did not settle near demand with headroom", c.rate)
	}
	c.provenRate, c.provenUntil = 8000, now.Add(time.Second)
	before := c.rate
	c.resume(now)
	if c.rate != before {
		t.Fatal("a single burst restored oversized remembered capacity")
	}
}

func TestEconomyStillGrowsAndBacksOff(t *testing.T) {
	now := time.Unix(1000, 0)
	s := new(Stats)
	c := newExchangeControl(200, 8000, now, s)
	c.economy = true
	for epoch := 0; epoch < 5; epoch++ {
		now = now.Add(100 * time.Millisecond)
		for n := 0; n < c.rate/10; n++ {
			c.reply(now, 90*time.Millisecond, true, true)
		}
		c.update(now, true)
	}
	if c.rate != 8000 {
		t.Fatal("sustained demand did not grow", c.rate)
	}
	now = now.Add(100 * time.Millisecond)
	for n := 0; n < 80; n++ {
		c.reply(now, 90*time.Millisecond, false, false)
	}
	c.lost = 20
	c.update(now, false)
	if c.rate != 5600 || s.ExchangeLossBackoffs.Load() != 1 {
		t.Fatal("economy masked congestion", c.rate)
	}
	// With no authenticated replies, a backlog alone cannot increase rate.
	c = newExchangeControl(200, 8000, now, new(Stats))
	c.economy = true
	for i := 0; i < 10; i++ {
		now = now.Add(time.Second)
		c.update(now, true)
	}
	if c.rate != 200 {
		t.Fatal("grew without authenticated feedback")
	}
}

func TestEconomyUploadResumesOnlyObservedDemand(t *testing.T) {
	now := time.Unix(1000, 0)
	c := newExchangeControl(200, 8000, now, new(Stats))
	c.economy, c.provenRate, c.provenUntil = true, 8000, now.Add(time.Second)
	c.lastReply, c.srtt, c.minRTT = now, 90*time.Millisecond, 90*time.Millisecond
	c.resumeUpload(now, 20)
	if c.rate != 1000 {
		t.Fatal("small upload burst restored oversized capacity", c.rate)
	}
	c.resumeUpload(now, 20)
	if c.rate != 1000 {
		t.Fatal("repeated queue observation multiplied demand", c.rate)
	}
	c.lost = 1
	c.resumeUpload(now, 100)
	if c.rate != 1000 {
		t.Fatal("upload bypassed loss response")
	}
	c.lost = 0
	c.resumeUpload(now.Add(2*time.Second), 100)
	if c.rate != 1000 {
		t.Fatal("upload used expired proven capacity")
	}
}
