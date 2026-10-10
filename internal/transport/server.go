package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"

	"customwireguard/internal/config"
	"customwireguard/internal/wire"
)

// RunServer owns conn and closes it on cancellation. The configured target is
// always loopback; one instance serves one WireGuard peer/outer secret.
func RunServer(ctx context.Context, conn datagramSocket, cfg config.Config, stats *Stats, diagnosticRate ...int) error {
	rate := 20
	if len(diagnosticRate) > 1 || (len(diagnosticRate) == 1 && (diagnosticRate[0] < 20 || diagnosticRate[0] > 200)) {
		conn.Close()
		return errors.New("diagnostic reply rate must be 20..200/s")
	}
	if len(diagnosticRate) == 1 {
		rate = diagnosticRate[0]
	}
	if err := cfg.Validate("server"); err != nil {
		conn.Close()
		return err
	}
	key, _ := cfg.Secret()
	exchange := cfg.Transport == "exchange"
	bootstrap, err := codecForMode(key, nil, false, exchange)
	if err != nil {
		conn.Close()
		return err
	}
	backendAddr, err := net.ResolveUDPAddr("udp", cfg.WireGuard)
	if err != nil {
		conn.Close()
		return err
	}
	backend, err := net.DialUDP("udp", nil, backendAddr)
	if err != nil {
		conn.Close()
		return err
	}
	// Kernel WireGuard can deliver a batch of native packets at once. Match
	// the public socket's receive-buffer request before starting the reader;
	// the OS default can overflow before our bounded userspace queue sees it.
	if err := backend.SetReadBuffer(2 << 20); err != nil {
		backend.Close()
		conn.Close()
		return fmt.Errorf("set WireGuard backend receive buffer: %w", err)
	}
	var capacities []int
	if cfg.ExchangeBurstMS > 0 {
		capacities = []int{256, 2048}
	}
	channels, cleanup := readersSized(ctx, stats, capacities, conn, backend)
	defer cleanup()
	var active, pending *association
	fecReceive := &fecReceiver{stats: stats, emit: func(data []byte) {
		if send(backend, zeroAddr, data, stats) {
			stats.Received.Add(1)
		}
	}}
	defer fecReceive.reset()
	fecSend := &fecSender{stats: stats}
	defer fecSend.reset()
	delivery := newExchangeReorder(cfg.ExchangeReorderMS, stats, func(data []byte) {
		if active != nil && active.fec {
			fecReceive.push(data, time.Now())
		} else {
			fecReceive.emit(data)
		}
	})
	defer delivery.reset()
	repair := exchangeRepair{enabled: cfg.ExchangeRepair, stats: stats}
	defer repair.reset()
	pacer := newPacketPacer(cfg, conn, stats)
	defer pacer.reset()
	downstream := newExchangeQueue(cfg, stats)
	bootstrapLimit := bucket{rate: 100, burst: 200}
	welcomeLimit := bucket{rate: 2, burst: 4}
	pingLimit := bucket{rate: float64(rate), burst: float64(rate) * 1.5}
	type seenBootstrap struct {
		addr    netip.AddrPort
		kind    byte
		expires time.Time
		fec     bool
	}
	history := make(map[wire.ID]seenBootstrap)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-delivery.ready():
			delivery.wake(time.Now())
		case <-pacer.ready():
			if active == nil || time.Since(active.lastSeen) >= 3*time.Minute {
				pacer.reset()
			} else {
				pacer.drain(time.Now())
			}
		case p := <-channels[0]:
			now := time.Now()
			var a *association
			var f wire.Frame
			var ok, newest bool
			if active != nil && now.Sub(active.lastSeen) < 3*time.Minute {
				f, ok, newest = active.open(p.data, now)
				if ok {
					a = active
				}
			}
			if !ok && pending != nil && now.Sub(pending.created) < 15*time.Second {
				f, ok, newest = pending.open(p.data, now)
				if ok && (f.Kind == wire.Data || f.Kind == wire.Ping || f.Kind == wire.Exchange) {
					pacer.reset()
					downstream.clear()
					active, pending = pending, nil
					delivery.reset()
					repair.reset()
					fecSend.reset()
					fecReceive.reset()
					a = active
					stats.Established.Add(1)
				} else {
					ok = false
				}
			}
			if ok && a != nil {
				// An older, reordered packet must not move the response endpoint back.
				if newest {
					learnAuthenticated(conn, p.addr)
					if a.addr != p.addr {
						pacer.rebind(p.addr)
					}
					a.addr = p.addr
				}
				switch f.Kind {
				case wire.Exchange:
					stats.ExchangeRequests.Add(1)
					if !newest {
						stats.ExchangeReorderedRequests.Add(1)
					}
					delivery.push(f.Sequence, f.Payload, now)
					var payload []byte
					parity := false
					if a.fec {
						payload, parity = fecSend.next(&downstream, now)
					} else {
						payload = downstream.pop(now)
					}
					backup := false
					if len(payload) == 0 {
						payload = repair.pop(now)
						backup = len(payload) > 0
					}
					reply := make([]byte, 9+len(payload))
					binary.BigEndian.PutUint64(reply[:8], f.Sequence)
					if downstream.length() > 0 {
						reply[8] = 1
					}
					copy(reply[9:], payload)
					out, e := a.sealSized(wire.ExchangeReply, reply, len(p.data), now)
					// Exactly one same-size reply, to this request's source. Even
					// reordered requests never use a different cached destination.
					if e == nil && send(conn, p.addr, out, stats) {
						stats.ExchangeReplies.Add(1)
						if len(payload) > 0 {
							if parity {
								stats.ExchangeFECParity.Add(1)
							} else {
								stats.Sent.Add(1)
								if a.fec && len(payload) >= fecHeader && binary.BigEndian.Uint32(payload) == fecMagic {
									stats.ExchangeFECData.Add(1)
								}
							}
							if backup {
								stats.ExchangeRepairSent.Add(1)
							} else {
								repair.remember(payload, now)
							}
						} else {
							stats.ExchangeEmpty.Add(1)
						}
					}
				case wire.Data:
					if send(backend, zeroAddr, f.Payload, stats) {
						stats.Received.Add(1)
					}
				case wire.Ping:
					if !pingLimit.allow(now) {
						continue
					}
					// Match the request length, so probes cannot amplify traffic.
					a.send++
					out, e := a.codec.Seal(wire.Frame{Kind: wire.Pong, ID: a.id, Sequence: a.send, Timestamp: now.Unix(), Payload: f.Payload}, len(p.data))
					if e == nil {
						send(conn, a.addr, out, stats)
					}
				default:
					stats.Rejected.Add(1)
				}
				continue
			}
			if !bootstrapLimit.allow(now) {
				stats.Rejected.Add(1)
				continue
			}
			f, err = bootstrap.Open(p.data, now)
			fecRequested := cfg.ExchangeFEC && bytes.Equal(f.Payload, fecCapability)
			if err != nil || (f.Kind != wire.Hello && f.Kind != wire.Probe) || f.Sequence != 0 || (len(f.Payload) != 0 && !fecRequested) || len(p.data) < 256 || (f.Kind == wire.Hello && len(p.data) > 512) || f.ID == (wire.ID{}) {
				stats.Rejected.Add(1)
				continue
			}
			if !welcomeLimit.allow(now) {
				continue
			}
			for sid, seen := range history {
				if now.After(seen.expires) {
					delete(history, sid)
				}
			}
			if seen, exists := history[f.ID]; exists {
				if seen.addr != p.addr || seen.kind != f.Kind || seen.fec != fecRequested || f.Kind == wire.Probe {
					stats.Rejected.Add(1)
					continue
				}
			} else {
				if len(history) >= 512 {
					stats.Rejected.Add(1)
					continue
				}
				history[f.ID] = seenBootstrap{p.addr, f.Kind, now.Add(155 * time.Second), fecRequested}
			}
			learnAuthenticated(conn, p.addr)
			if f.Kind == wire.Probe {
				out, e := bootstrap.Seal(wire.Frame{Kind: wire.ProbeReply, ID: f.ID, Timestamp: now.Unix(), Payload: fecBootstrap(fecRequested)}, len(p.data))
				if e == nil {
					send(conn, p.addr, out, stats)
				}
				continue
			}
			// An active ID can only recover its existing welcome from its current
			// address. A new connection needs a new random ID and fresh challenge.
			var candidate *association
			if active != nil && active.id == f.ID && now.Sub(active.lastSeen) < 3*time.Minute {
				if active.addr != p.addr {
					continue
				}
				candidate = active
			} else if pending != nil && pending.id == f.ID && pending.addr == p.addr && now.Sub(pending.created) < 15*time.Second {
				candidate = pending
			} else {
				token := make([]byte, 32)
				rand.Read(token)
				pending, err = newAssociationMode(key, f.ID, token, false, now, exchange)
				if err != nil {
					return err
				}
				pending.addr = p.addr
				pending.fec = fecRequested
				candidate = pending
			}
			if candidate.fec != fecRequested {
				stats.Rejected.Add(1)
				continue
			}
			if !candidate.lastWelcome.IsZero() && now.Sub(candidate.lastWelcome) < 500*time.Millisecond {
				continue
			}
			candidate.lastWelcome = now
			out, e := bootstrap.Seal(wire.Frame{Kind: wire.Welcome, ID: f.ID, Timestamp: now.Unix(), Payload: candidate.token}, len(p.data))
			if e == nil {
				send(conn, p.addr, out, stats)
			}
		case p := <-channels[1]:
			now := time.Now()
			if active == nil || now.Sub(active.lastSeen) >= 3*time.Minute {
				continue
			}
			if exchange {
				if len(p.data) == 0 || len(p.data) > wire.ExchangeMaxPayload {
					stats.Oversize.Add(1)
				} else {
					downstream.push(p.data, p.at)
				}
				continue
			}
			out, e := active.seal(wire.Data, p.data, cfg.Profile, now)
			if e != nil {
				stats.Oversize.Add(1)
				continue
			}
			pacer.push(out, active.addr, now)
		}
	}
}

func learnAuthenticated(conn datagramSocket, addr netip.AddrPort) {
	if learned, ok := conn.(interface{ authenticated(netip.AddrPort) }); ok {
		learned.authenticated(addr)
	}
}
