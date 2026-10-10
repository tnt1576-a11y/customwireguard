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

func runAdaptiveExchangeClient(ctx context.Context, local, remote *net.UDPConn, cfg config.Config, stats *Stats) error {
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
	var capacities []int
	if cfg.ExchangeBurstMS > 0 {
		// Match the server's native input handoff. At higher rates a native
		// batch can exceed 256 packets before the event loop drains it; losing
		// here would discard small packets before priority can be applied.
		capacities = []int{2048, 256}
	}
	channels, cleanup := readersSized(ctx, stats, capacities, local, remote)
	defer cleanup()
	delivery := newExchangeReorder(cfg.ExchangeReorderMS, stats, func(data []byte) {
		if send(local, wgAddr, data, stats) {
			stats.Received.Add(1)
		}
	})
	defer delivery.reset()
	repair := exchangeRepair{enabled: cfg.ExchangeRepair, stats: stats}
	defer repair.reset()
	if cfg.ExchangeQueueMS == 0 {
		cfg.ExchangeQueueMS = 60
	}
	upload := newExchangeQueue(cfg, stats)
	// The local WireGuard handshake may precede our first outer handshake.
	// Give that bounded startup queue one second, then start the normal deadline
	// when the association is ready; otherwise its first handshake is discarded.
	upload.maxAge = time.Second
	defer upload.clear()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	poll := time.NewTimer(time.Hour)
	poll.Stop()
	defer poll.Stop()
	var active *association
	var nextHello, nextPoll, busyUntil time.Time
	attempt := time.Now()
	id := wire.NewID()
	flights := new(exchangeFlights)
	control := newExchangeControl(cfg.ExchangePPS, cfg.ExchangeMaxPPS, attempt, stats)
	pacer := exchangePacer{}
	defer stats.ExchangePending.Store(0)
	defer stats.ExchangeRatePPS.Store(0)
	arm := func(now time.Time, delay time.Duration) { nextPoll = now.Add(delay); poll.Reset(delay) }
	busy := func(now time.Time) {
		busyUntil = now.Add(2 * time.Second)
		if active != nil && nextPoll.After(now.Add(time.Millisecond)) {
			arm(now, time.Millisecond)
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
		case <-delivery.ready():
			delivery.wake(time.Now())
		case now := <-tick.C:
			if active != nil && now.Sub(active.lastSeen) > 15*time.Second {
				active = nil
				delivery.reset()
				repair.reset()
				poll.Stop()
				upload.clear()
				upload.maxAge = time.Second
				flights = new(exchangeFlights)
				control = newExchangeControl(cfg.ExchangePPS, cfg.ExchangeMaxPPS, now, stats)
				pacer = exchangePacer{}
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
			lost, currentLost := flights.expire(now, control.timeout(), control.delayReduction)
			control.lost += currentLost
			stats.ExchangeExpired.Add(uint64(lost))
			stats.ExchangePriorFlightExpired.Add(uint64(lost - currentLost))
			upload.expire(now)
			control.update(now, upload.length() > 0)
			rate := control.rate
			if now.After(busyUntil) && upload.length() == 0 {
				rate = 4
			}
			stats.ExchangeRatePPS.Store(uint64(rate))
			count := pacer.ready(now, rate)
			for i := 0; i < count; i++ {
				if flights.count >= control.window() || !flights.available(active.send+1) {
					stats.ExchangeWindowFull.Add(1)
					break
				}
				at := time.Now()
				payload := upload.pop(at)
				backup := false
				if len(payload) == 0 {
					payload = repair.pop(at)
					backup = len(payload) > 0
				}
				size := wire.RandomBetween(wire.ExchangeMinSize, wire.MaxDatagram)
				out, e := active.sealSized(wire.Exchange, payload, size, at)
				if e != nil {
					return e
				}
				if send(remote, zeroAddr, out, stats) {
					flights.add(active.send, at, size)
					stats.ExchangeRequests.Add(1)
					if len(payload) > 0 {
						stats.Sent.Add(1)
						if backup {
							stats.ExchangeRepairSent.Add(1)
						} else {
							repair.remember(payload, at)
						}
					}
				}
			}
			stats.ExchangePending.Store(uint64(flights.count))
			arm(time.Now(), pacer.wait())
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
			if upload.length() >= 16 {
				control.resume(now)
			}
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
				upload.expire(now)
				upload.rebase(now)
				upload.maxAge = time.Duration(cfg.ExchangeQueueMS) * time.Millisecond
				busyUntil = now.Add(2 * time.Second)
				arm(now, 0)
				continue
			}
			f, ok, newest := active.open(p.data, now)
			if !ok || f.Kind != wire.ExchangeReply {
				stats.Rejected.Add(1)
				continue
			}
			if !newest {
				stats.ExchangeReorderedReplies.Add(1)
			}
			seq := binary.BigEndian.Uint64(f.Payload[:8])
			flight, exists := flights.take(seq, len(p.data))
			if !exists {
				stats.ExchangeLateReplies.Add(1)
				continue
			}
			stats.ExchangePending.Store(uint64(flights.count))
			stats.ExchangeReplies.Add(1)
			useful := len(f.Payload) > 9
			control.reply(now, now.Sub(flight.at), useful, f.Payload[8] != 0)
			delivery.push(f.Sequence, f.Payload[9:], now)
			if useful {
				busy(now)
			} else {
				stats.ExchangeEmpty.Add(1)
			}
			if f.Payload[8] != 0 {
				control.resume(now)
				busy(now)
			}
		}
	}
}
