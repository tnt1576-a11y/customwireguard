package transport

import (
	"context"
	"net"
	"net/netip"
	"time"

	"customwireguard/internal/config"
	"customwireguard/internal/wire"
)

var zeroAddr netip.AddrPort

// RunClient owns local and remote. All state changes take place in one event
// loop; local packets are only accepted from the configured WireGuard port.
func RunClient(ctx context.Context, local, remote *net.UDPConn, cfg config.Config, stats *Stats) error {
	if err := cfg.Validate("client"); err != nil {
		local.Close()
		remote.Close()
		return err
	}
	key, _ := cfg.Secret()
	bootstrap, err := wire.NewCodec(key, nil, true)
	if err != nil {
		local.Close()
		remote.Close()
		return err
	}
	wgAddr := netip.MustParseAddrPort(cfg.WireGuard)
	channels, cleanup := readers(ctx, stats, local, remote)
	defer cleanup()
	pacer := newPacketPacer(cfg, remote, stats)
	defer pacer.reset()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	var active *association
	id := wire.NewID()
	attemptStarted := time.Now()
	var nextHello, nextPing time.Time
	var queued [][]byte
	reset := func() {
		pacer.reset()
		active = nil
		id = wire.NewID()
		attemptStarted = time.Now()
		nextHello = time.Time{}
		queued = nil
	}
	transmit := func(kind byte, payload []byte, now time.Time) {
		out, e := active.seal(kind, payload, cfg.Profile, now)
		if e != nil {
			stats.Oversize.Add(1)
			return
		}
		if kind == wire.Data {
			pacer.push(out, zeroAddr, now)
		} else {
			send(remote, zeroAddr, out, stats)
		}
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-pacer.ready():
			pacer.drain(time.Now())
		case now := <-ticker.C:
			if active != nil && now.Sub(active.lastSeen) > 70*time.Second {
				reset()
			}
			if active == nil {
				if now.Sub(attemptStarted) > 15*time.Second {
					id = wire.NewID()
					attemptStarted = now
					nextHello = time.Time{}
				}
				if now.Before(nextHello) {
					continue
				}
				out, e := bootstrap.Seal(wire.Frame{Kind: wire.Hello, ID: id, Timestamp: now.Unix()}, wire.RandomBetween(256, 512))
				if e != nil {
					return e
				}
				send(remote, zeroAddr, out, stats)
				nextHello = now.Add(time.Duration(wire.RandomBetween(1700, 3000)) * time.Millisecond)
			} else if !now.Before(nextPing) {
				transmit(wire.Ping, pingPayload(), now)
				nextPing = now.Add(time.Duration(wire.RandomBetween(20, 30)) * time.Second)
			}
		case p := <-channels[0]:
			if p.addr != wgAddr {
				stats.Rejected.Add(1)
				continue
			}
			if len(p.data) == 0 || len(p.data) > wire.MaxPayload {
				stats.Oversize.Add(1)
				continue
			}
			if active == nil {
				if len(queued) < 64 {
					queued = append(queued, p.data)
				} else {
					stats.QueueDropped.Add(1)
				}
				continue
			}
			transmit(wire.Data, p.data, time.Now())
		case p := <-channels[1]:
			now := time.Now()
			if active == nil {
				f, e := bootstrap.Open(p.data, now)
				if e != nil || f.Kind != wire.Welcome || f.ID != id || f.Sequence != 0 || len(f.Payload) != 32 || len(p.data) < 256 || len(p.data) > 512 {
					stats.Rejected.Add(1)
					continue
				}
				active, e = newAssociation(key, id, f.Payload, true, now)
				if e != nil {
					return e
				}
				stats.Established.Add(1)
				transmit(wire.Ping, pingPayload(), now)
				nextPing = now.Add(time.Duration(wire.RandomBetween(20, 30)) * time.Second)
				for _, payload := range queued {
					transmit(wire.Data, payload, now)
				}
				queued = nil
				continue
			}
			f, ok, _ := active.open(p.data, now)
			if !ok {
				stats.Rejected.Add(1)
				continue
			}
			if f.Kind == wire.Data {
				if send(local, wgAddr, f.Payload, stats) {
					stats.Received.Add(1)
				}
			} else if f.Kind != wire.Pong {
				stats.Rejected.Add(1)
			}
		}
	}
}
