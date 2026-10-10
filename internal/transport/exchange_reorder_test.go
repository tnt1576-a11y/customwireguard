package transport

import (
	"reflect"
	"testing"
	"time"
)

func TestExchangeReorderRestoresInversionsWithoutDelayingOrderedData(t *testing.T) {
	now := time.Unix(1000, 0)
	var got []byte
	s := new(Stats)
	r := newExchangeReorder(2, s, func(p []byte) { got = append(got, p...) })
	defer r.reset()
	r.push(1, []byte{1}, now)
	if !reflect.DeepEqual(got, []byte{1}) || r.ready() != nil {
		t.Fatal("in-order delivery delayed")
	}
	r.push(4, []byte{4}, now)
	r.push(3, []byte{3}, now.Add(time.Millisecond))
	r.push(3, []byte{3}, now.Add(time.Millisecond)) // pending duplicate cannot replace its arrival time
	if len(got) != 1 || r.count != 2 {
		t.Fatal("gap not held or duplicate retained")
	}
	r.push(2, []byte{2}, now.Add(1500*time.Microsecond))
	if !reflect.DeepEqual(got, []byte{1, 2, 3, 4}) || r.count != 0 || r.ready() != nil {
		t.Fatal("order not restored", got)
	}
	if s.ExchangeReorderHeld.Load() != 2 || s.ExchangeReorderWaitMaxUS.Load() != 1500 {
		t.Fatal("hold accounting", s.Snapshot())
	}
}

func TestExchangeReorderLossDeadlineAndLateDelivery(t *testing.T) {
	now := time.Unix(1000, 0)
	var got []byte
	s := new(Stats)
	r := newExchangeReorder(2, s, func(p []byte) { got = append(got, p...) })
	defer r.reset()
	r.push(2, []byte{2}, now)
	r.push(3, nil, now.Add(time.Millisecond))
	r.push(4, []byte{4}, now.Add(time.Millisecond))
	r.wake(now.Add(1999 * time.Microsecond))
	if len(got) != 0 {
		t.Fatal("deadline shortened")
	}
	r.wake(now.Add(2 * time.Millisecond))
	if !reflect.DeepEqual(got, []byte{2, 4}) || r.count != 0 {
		t.Fatal("missing packet blocked delivery", got)
	}
	r.push(1, []byte{1}, now.Add(3*time.Millisecond))
	if !reflect.DeepEqual(got, []byte{2, 4, 1}) || s.ExchangeReorderLate.Load() != 1 {
		t.Fatal("authenticated late payload lost")
	}
}

func TestExchangeReorderBoundsResetAndDisabled(t *testing.T) {
	now := time.Unix(1000, 0)
	got := 0
	s := new(Stats)
	r := newExchangeReorder(2, s, func(p []byte) { got++ })
	defer r.reset()
	for seq := uint64(2); seq <= 128; seq++ {
		r.push(seq, []byte{1}, now)
	}
	if r.count != 127 || got != 0 {
		t.Fatal("window changed")
	}
	r.push(1<<40, []byte{1}, now)
	if got != 128 || r.count != 0 || s.ExchangeReorderForced.Load() != 1 {
		t.Fatal("large gap lost buffered data or exceeded bound")
	}
	r.push((1<<40)+2, []byte{1}, now)
	r.reset()
	r.wake(now.Add(time.Second))
	if got != 128 || r.next != 1 || r.count != 0 || r.ready() != nil {
		t.Fatal("old association data survived reset")
	}
	r.push(1, []byte{1}, now)
	if got != 129 {
		t.Fatal("new association blocked")
	}
	disabled := newExchangeReorder(0, new(Stats), func(p []byte) { got++ })
	for _, seq := range []uint64{5, 3, 1000} {
		disabled.push(seq, []byte{1}, now)
	}
	if got != 132 || disabled.count != 0 || disabled.ready() != nil {
		t.Fatal("disabled mode buffered data")
	}
}

func TestExchangeReorderTimerReleasesWithoutFurtherPackets(t *testing.T) {
	got := false
	r := newExchangeReorder(2, new(Stats), func(p []byte) { got = true })
	defer r.reset()
	r.push(2, []byte{1}, time.Now())
	select {
	case <-r.ready():
		r.wake(time.Now())
	case <-time.After(time.Second):
		t.Fatal("silent gap never woke the event loop")
	}
	if !got || r.ready() != nil {
		t.Fatal("timer failed to drain")
	}
}
