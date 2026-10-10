package transport

import (
	"customwireguard/internal/config"
	"testing"
	"time"
)

func TestExchangeBurstDrainsWithoutDelayingSmallPackets(t *testing.T) {
	now := time.Unix(1000, 0)
	s := new(Stats)
	q := newExchangeQueue(config.Config{ExchangeQueueMS: 60, ExchangeBurstMS: 200}, s)
	// A 1000-packet application burst needs ~167 ms at the configured ceiling.
	for i := 0; i < 1000; i++ {
		q.push(make([]byte, 1120), now)
	}
	bulk, small := 0, 0
	for ms := 0; ms < 180; ms++ {
		at := now.Add(time.Duration(ms) * time.Millisecond)
		if ms%10 == 0 {
			q.push(make([]byte, 80), at)
		}
		for n := 0; n < 6; n++ {
			p := q.pop(at)
			if len(p) == 80 {
				small++
			} else if len(p) > 0 {
				bulk++
			}
		}
	}
	if bulk != 1000 || small != 18 || s.ExchangeQueueDropped.Load() != 0 {
		t.Fatal("burst lost payload", bulk, small, s.Snapshot())
	}
	if s.ExchangeSmallDelayMaxUS.Load() > 1000 || s.ExchangeQueueDelayMaxUS.Load() < 60000 {
		t.Fatal("small packets did not bypass bulk", s.Snapshot())
	}
}

func TestExchangeBurstBoundFairnessAndExpiry(t *testing.T) {
	now := time.Now()
	s := new(Stats)
	q := newExchangeQueue(config.Config{ExchangeQueueMS: 60, ExchangeBurstMS: 200}, s)
	for i := 0; i < 2200; i++ {
		q.push(make([]byte, 1120), now)
	}
	for i := 0; i < 200; i++ {
		q.push(make([]byte, 80), now)
	}
	if q.length() != 2048 || len(q.small) != 128 || s.ExchangeQueueFull.Load() != 352 {
		t.Fatal("queue reservation unbounded", q.length(), len(q.small), s.Snapshot())
	}
	for n := 0; n < 10; n++ {
		p := q.pop(now)
		if (n%5 == 4) != (len(p) == 1120) {
			t.Fatal("bulk starved", n, len(p))
		}
	}
	q.expire(now.Add(61 * time.Millisecond))
	if len(q.small) != 0 || len(q.items) != 1918 {
		t.Fatal("wrong per-class expiry")
	}
	q.expire(now.Add(201 * time.Millisecond))
	if q.length() != 0 || s.ExchangeQueuePackets.Load() != 0 || s.ExchangeSmallQueuePackets.Load() != 0 {
		t.Fatal("sustained backlog exceeded hard deadline")
	}
	q.push(make([]byte, 80), now)
	q.push(make([]byte, 1120), now)
	q.rebase(now.Add(time.Second))
	if q.pop(now.Add(time.Second)) == nil {
		t.Fatal("startup rebasing lost small packet")
	}
	q.clear()
	if q.length() != 0 || q.smallRun != 0 {
		t.Fatal("clear retained state")
	}
}

func TestExchangeLargerUploadBurst(t *testing.T) {
	for _, deadline := range []int{200, 350} {
		now := time.Unix(1000, 0)
		s := new(Stats)
		q := newExchangeQueue(config.Config{ExchangeQueueMS: 60, ExchangeBurstMS: deadline}, s)
		// The observed upload peak was 1874 packets, within the 1920 bulk cap.
		// Include arrival/handoff time in its age, plus new small traffic.
		for n := 0; n < 1874; n++ {
			q.push(make([]byte, 1120), now)
		}
		bulk, small := 0, 0
		for ms := 5; ms < 345; ms++ {
			at := now.Add(time.Duration(ms) * time.Millisecond)
			if ms%10 == 0 {
				q.push(make([]byte, 80), at)
			}
			for n := 0; n < 6; n++ {
				switch len(q.pop(at)) {
				case 1120:
					bulk++
				case 80:
					small++
				}
			}
		}
		if small != 34 || s.ExchangeSmallDelayMaxUS.Load() > 1000 || s.ExchangeQueueFull.Load() != 0 {
			t.Fatal("small traffic delayed or queue overflow", deadline, s.Snapshot())
		}
		if deadline == 200 && s.ExchangeQueueExpired.Load() == 0 {
			t.Fatal("old deadline failed to reproduce burst loss")
		}
		if deadline == 350 && (bulk != 1874 || s.ExchangeQueueExpired.Load() != 0) {
			t.Fatal("extended deadline lost measured burst", s.Snapshot())
		}
		q.push(make([]byte, 1120), now)
		q.push(make([]byte, 80), now)
		q.expire(now.Add(time.Duration(deadline+1) * time.Millisecond))
		if q.length() != 0 {
			t.Fatal("hard deadline no longer bounds stale ingress")
		}
	}
}

func TestExchangeThreeThousandPacketBurstWithSmallTraffic(t *testing.T) {
	now := time.Unix(1000, 0)
	s := new(Stats)
	q := newExchangeQueue(config.Config{ExchangeQueueMS: 60, ExchangeBurstMS: 350, ExchangeQueuePackets: 4096}, s)
	for n := 0; n < 3000; n++ {
		q.push(make([]byte, 1120), now)
	}
	bulk, small := 0, 0
	for ms := 0; ms < 350; ms++ {
		at := now.Add(time.Duration(ms) * time.Millisecond)
		if ms%10 == 0 {
			q.push(make([]byte, 80), at)
		}
		for n := 0; n < 9; n++ {
			switch len(q.pop(at)) {
			case 1120:
				bulk++
			case 80:
				small++
			}
		}
	}
	if bulk != 3000 || small != 35 || s.ExchangeQueueDropped.Load() != 0 || s.ExchangeSmallDelayMaxUS.Load() > 1000 {
		t.Fatal("burst or small priority regressed", bulk, small, s.Snapshot())
	}
	for n := 0; n < 5000; n++ {
		q.push(make([]byte, 1120), now)
	}
	for n := 0; n < 200; n++ {
		q.push(make([]byte, 80), now)
	}
	if q.length() != 4096 || len(q.small) != 128 {
		t.Fatal("capacity or small reservation unbounded")
	}
	q.expire(now.Add(351 * time.Millisecond))
	if q.length() != 0 {
		t.Fatal("deadline no longer bounds overload")
	}
}
