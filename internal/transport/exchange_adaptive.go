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
	channels, cleanup := readersSized(ctx, stats, adaptiveIngressCapacities(cfg), local, remote)
	defer cleanup()
	fecReceive := &fecReceiver{stats: stats, emit: func(data []byte) {
		if send(local, wgAddr, data, stats) {
			stats.Received.Add(1)
		}
	}}
	defer fecReceive.reset()
	fecSend := &fecSender{stats: stats}
	defer fecSend.reset()
	delivery := newExchangeReorder(cfg.ExchangeReorderMS, stats, func(data []byte) {
		if cfg.ExchangeFEC {
			fecReceive.push(data, time.Now())
		} else {
			fecReceive.emit(data)
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
	var grant clientGrant
	var nextHello, nextPoll, busyUntil time.Time
	attempt := time.Now()
	id := wire.NewID()
	flights := new(exchangeFlights)
	control := newExchangeControl(cfg.ExchangePPS, cfg.ExchangeMaxPPS, attempt, stats)
	control.economy = cfg.ExchangeEconomy
	pacer := exchangePacer{}
	// Only this event loop owns these buffers. send completes synchronously;
	// flights retain metadata, while repair retains the separate native payload.
	txBuffer := make([]byte, wire.MaxDatagram)
	requestBuffer := make([]byte, wire.ExchangeMaxPayload+4)
	defer stats.ExchangePending.Store(0)
	defer stats.ExchangeRatePPS.Store(0)
	defer stats.ExchangeGrantPPS.Store(0)
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
				fecSend.reset()
				fecReceive.reset()
				poll.Stop()
				upload.clear()
				upload.maxAge = time.Second
				flights = new(exchangeFlights)
				control = newExchangeControl(cfg.ExchangePPS, cfg.ExchangeMaxPPS, now, stats)
				control.economy = cfg.ExchangeEconomy
				pacer = exchangePacer{}
				grant = clientGrant{}
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
			capability := fecBootstrap(cfg.ExchangeFEC)
			if cfg.ExchangeShared {
				capability = sharedCapability
			}
			out, e := bootstrap.Seal(wire.Frame{Kind: wire.Hello, ID: id, Timestamp: now.Unix(), Payload: capability}, wire.RandomBetween(256, 512))
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
			desired := rate
			if cfg.ExchangeShared {
				limit := grant.limit(now)
				rate = min(rate, limit)
				stats.ExchangeGrantPPS.Store(uint64(limit))
			}
			if now.After(busyUntil) && upload.length() == 0 {
				rate = 4
				desired = sharedMinimum * 100
			}
			stats.ExchangeRatePPS.Store(uint64(rate))
			count := pacer.ready(now, rate)
			for i := 0; i < count; i++ {
				if flights.count >= control.window() || !flights.available(active.send+1) {
					stats.ExchangeWindowFull.Add(1)
					break
				}
				at := time.Now()
				var payload []byte
				parity := false
				if cfg.ExchangeFEC {
					payload, parity = fecSend.next(&upload, at)
				} else {
					payload = upload.pop(at)
				}
				backup := false
				if len(payload) == 0 {
					payload = repair.pop(at)
					backup = len(payload) > 0
				}
				size := wire.RandomBetween(wire.ExchangeMinSize, wire.MaxDatagram)
				body := payload
				if cfg.ExchangeShared {
					body = sharedRequestInto(requestBuffer, payload, grant.version, desired, cfg.ExchangeMaxPPS)
				}
				out, e := active.sealSizedInto(txBuffer, wire.Exchange, body, size, at)
				if e != nil {
					return e
				}
				if send(remote, zeroAddr, out, stats) {
					flights.add(active.send, at, size)
					stats.ExchangeRequests.Add(1)
					stats.ExchangeRequestBytes.Add(uint64(len(out)))
					if len(payload) > 0 {
						if parity {
							stats.ExchangeFECParity.Add(1)
						} else {
							stats.Sent.Add(1)
							if cfg.ExchangeFEC && len(payload) >= fecHeader && binary.BigEndian.Uint32(payload) == fecMagic {
								stats.ExchangeFECData.Add(1)
							}
						}
						if backup {
							stats.ExchangeRepairSent.Add(1)
						} else {
							control.uploaded++
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
				control.resumeUpload(now, upload.length())
			}
			busy(now)
		case p := <-channels[1]:
			now := time.Now()
			if active == nil {
				f, e := bootstrap.Open(p.data, now)
				welcomeSize := 32
				if cfg.ExchangeShared {
					welcomeSize = 33
				}
				if e != nil || f.Kind != wire.Welcome || f.ID != id || f.Sequence != 0 || len(f.Payload) != welcomeSize || (cfg.ExchangeShared && f.Payload[32] != 1) || len(p.data) < 256 || len(p.data) > 512 {
					stats.Rejected.Add(1)
					continue
				}
				active, e = newAssociationMode(key, id, f.Payload[:32], true, now, true)
				if e != nil {
					return e
				}
				stats.Established.Add(1)
				active.shared = cfg.ExchangeShared
				upload.expire(now)
				upload.rebase(now)
				upload.maxAge = time.Duration(cfg.ExchangeQueueMS) * time.Millisecond
				busyUntil = now.Add(2 * time.Second)
				arm(now, 0)
				continue
			}
			// pump supplies an independent allocation for each packet. Delivery
			// may retain its payload across reordering; that backing array is never
			// recycled here. Bootstrap and multi-user fanout remain non-mutating.
			f, ok, newest := active.openInPlace(p.data, now)
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
			stats.ExchangeReplyBytes.Add(uint64(len(p.data)))
			header := 9
			if cfg.ExchangeShared {
				header = 12
				if newest {
					// Anchor validity to our request's send time. A delayed reply
					// must not renew a grant after the server may reclaim it.
					grant.accept(binary.BigEndian.Uint16(f.Payload[9:11]), int(f.Payload[11]), flight.at)
				}
			}
			useful := len(f.Payload) > header
			control.reply(now, now.Sub(flight.at), useful, f.Payload[8] != 0)
			delivery.push(f.Sequence, f.Payload[header:], now)
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

func adaptiveIngressCapacities(cfg config.Config) []int {
	if cfg.ExchangeBurstMS == 0 {
		return nil
	}
	// Native batches must reach the priority queue before its deadline policy
	// runs. The connected outer socket also needs room for a coalesced reply
	// burst after a short scheduling stall. Neither handoff waits for a batch
	// to fill. Server public unauthenticated input remains separately bounded.
	return []int{2048, 1024}
}
