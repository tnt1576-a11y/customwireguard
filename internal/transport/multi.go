package transport

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"customwireguard/internal/config"
	"customwireguard/internal/wire"
)

const multiQueueCapacity = 64
const multiQueueAge = 5 * time.Millisecond

type MultiStats struct {
	Ingress        Stats
	Dispatched     atomic.Uint64
	UnknownDropped atomic.Uint64
	QueueDropped   atomic.Uint64
	QueueExpired   atomic.Uint64
	Routes         atomic.Uint64
	ReloadRejected atomic.Uint64
	Revision       atomic.Uint64
	mu             sync.RWMutex
	users          map[string]*Stats
}

func (s *MultiStats) Snapshot() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	users := map[string]any{}
	for name, stats := range s.users {
		users[name] = stats.Snapshot()
	}
	return map[string]any{"users": users, "ingress": s.Ingress.Snapshot(), "dispatched": s.Dispatched.Load(), "unknown_dropped": s.UnknownDropped.Load(), "queue_dropped": s.QueueDropped.Load(), "queue_expired": s.QueueExpired.Load(), "routes": s.Routes.Load(), "reload_rejected": s.ReloadRejected.Load(), "revision": s.Revision.Load()}
}

type multiLearn struct {
	user *multiUser
	addr netip.AddrPort
}
type multiDone struct {
	user *multiUser
	err  error
}
type multiUser struct {
	spec      config.ServerUser
	socket    *userSocket
	cancel    context.CancelFunc
	stats     *Stats
	queue     []packet
	addresses []netip.AddrPort
}
type userSocket struct {
	public   *net.UDPConn
	incoming chan packet
	closed   chan struct{}
	once     sync.Once
	user     *multiUser
	learns   chan<- multiLearn
	// Each of these fields has exactly one owner: the reader pump, or RunServer.
	arrival time.Time
	learned netip.AddrPort
}

func (s *userSocket) ReadFromUDPAddrPort(b []byte) (int, netip.AddrPort, error) {
	select {
	case <-s.closed:
		return 0, netip.AddrPort{}, net.ErrClosed
	default:
	}
	select {
	case <-s.closed:
		return 0, netip.AddrPort{}, net.ErrClosed
	case p := <-s.incoming:
		s.arrival = p.at
		return copy(b, p.data), p.addr, nil
	}
}
func (s *userSocket) receivedAt() time.Time { return s.arrival }
func (s *userSocket) WriteToUDPAddrPort(b []byte, addr netip.AddrPort) (int, error) {
	select {
	case <-s.closed:
		return 0, net.ErrClosed
	default:
		return s.public.WriteToUDPAddrPort(b, addr)
	}
}
func (s *userSocket) Write([]byte) (int, error) {
	return 0, errors.New("shared UDP socket requires a destination")
}
func (s *userSocket) LocalAddr() net.Addr  { return s.public.LocalAddr() }
func (s *userSocket) RemoteAddr() net.Addr { return nil }
func (s *userSocket) Close() error         { s.once.Do(func() { close(s.closed) }); return nil }
func (s *userSocket) authenticated(addr netip.AddrPort) {
	if s.learned == addr {
		return
	}
	select {
	case s.learns <- multiLearn{s.user, addr}:
		s.learned = addr
	case <-s.closed:
	default:
	}
}

// RunMultiServer retains the existing authenticated wire format. Unrecognized
// addresses and short bootstrap packets are tried against at most three bounded
// peer contexts. Only accepted, non-replayed authentication can populate the
// address cache. Established traffic needs one decryption, not one per user.
// Fair dispatch lends unused capacity while bounding total established requests.
// Reloading the same listener preserves every unchanged user's live session.
func RunMultiServer(ctx context.Context, conn *net.UDPConn, initial config.MultiServer, updates <-chan config.MultiServer, stats *MultiStats) error {
	if err := initial.Validate(); err != nil {
		conn.Close()
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	input, cleanup := readers(ctx, &stats.Ingress, conn)
	defer cleanup()
	learned := make(chan multiLearn, 128)
	finished := make(chan multiDone, 6)
	users := map[string]*multiUser{}
	var order []*multiUser
	known := map[netip.AddrPort]*multiUser{}
	var workers sync.WaitGroup
	defer func() {
		cancel()
		for _, u := range users {
			u.cancel()
			u.socket.Close()
		}
		workers.Wait()
	}()
	install := func(next config.MultiServer) error {
		if err := next.Validate(); err != nil {
			return err
		}
		if next.Listen != initial.Listen {
			return errors.New("cannot change shared listener during reload")
		}
		wanted := map[string]config.ServerUser{}
		for _, u := range next.Users {
			wanted[u.Name] = u
		}
		for name, u := range users {
			if spec, ok := wanted[name]; ok && spec == u.spec {
				continue
			}
			u.cancel()
			u.socket.Close()
			delete(users, name)
			for addr, owner := range known {
				if owner == u {
					delete(known, addr)
				}
			}
		}
		order = nil
		for _, spec := range next.Users {
			u := users[spec.Name]
			if u == nil {
				child, stop := context.WithCancel(ctx)
				u = &multiUser{spec: spec, cancel: stop, stats: new(Stats)}
				u.socket = &userSocket{public: conn, incoming: make(chan packet, 16), closed: make(chan struct{}), user: u, learns: learned}
				users[spec.Name] = u
				workers.Add(1)
				go func(member *multiUser) {
					defer workers.Done()
					err := RunServer(child, member.socket, member.spec.Config, member.stats)
					select {
					case finished <- multiDone{member, err}:
					case <-ctx.Done():
					}
				}(u)
			}
			order = append(order, u)
		}
		stats.mu.Lock()
		stats.users = map[string]*Stats{}
		for name, u := range users {
			stats.users[name] = u.stats
		}
		stats.mu.Unlock()
		stats.Routes.Store(uint64(len(known)))
		stats.Revision.Store(next.Revision)
		return nil
	}
	if err := install(initial); err != nil {
		return err
	}
	unknown := bucket{rate: 100, burst: 100}
	budget := bucket{rate: float64(initial.MaxPPS), burst: float64(max(8, initial.MaxPPS/250))}
	cursor := 0
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	var wake <-chan time.Time
	enqueue := func(u *multiUser, p packet) {
		if len(u.queue) >= multiQueueCapacity {
			stats.QueueDropped.Add(1)
			return
		}
		u.queue = append(u.queue, p)
	}
	drain := func(now time.Time) {
		pending := dispatchMulti(order, &cursor, &budget, stats, now)
		if pending && wake == nil {
			timer.Reset(time.Millisecond)
			wake = timer.C
		}
		if !pending && wake != nil {
			timer.Stop()
			wake = nil
		}
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case outcome := <-finished:
			if users[outcome.user.spec.Name] != outcome.user {
				continue
			}
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("user transport %s stopped: %v", outcome.user.spec.Name, outcome.err)
		case next, ok := <-updates:
			if !ok {
				updates = nil
				continue
			}
			if err := install(next); err != nil {
				stats.ReloadRejected.Add(1)
				continue
			}
			budget = bucket{rate: float64(next.MaxPPS), burst: float64(max(8, next.MaxPPS/250))}
			cursor = 0
		case event := <-learned:
			if users[event.user.spec.Name] != event.user {
				continue
			}
			u := event.user
			if known[event.addr] != u {
				if len(u.addresses) >= 4 {
					old := u.addresses[0]
					u.addresses = u.addresses[1:]
					if known[old] == u {
						delete(known, old)
					}
				}
				known[event.addr] = u
				u.addresses = append(u.addresses, event.addr)
				stats.Routes.Store(uint64(len(known)))
			}
		case p := <-input[0]:
			now := time.Now()
			if owner := known[p.addr]; owner != nil && len(p.data) >= wire.ExchangeMinSize {
				enqueue(owner, p)
			} else {
				// Short Hello packets always get bounded authentication fanout, so a NAT
				// reusing another user's old source port cannot strand a fresh session.
				if !unknown.allow(now) {
					stats.UnknownDropped.Add(1)
					continue
				}
				for _, u := range order {
					select {
					case u.socket.incoming <- p:
					default:
						stats.QueueDropped.Add(1)
					}
				}
			}
			drain(now)
		case <-wake:
			wake = nil
			drain(time.Now())
		}
	}
}

// dispatchMulti visits every active queue in turn, consuming shared tokens only
// for a selected packet. Keeping the cursor on exhaustion prevents starvation.
func dispatchMulti(order []*multiUser, cursor *int, budget *bucket, stats *MultiStats, now time.Time) bool {
	pending := false
	for pass := 0; pass < 64; pass++ {
		var selected *multiUser
		selectedIndex := 0
		for checked := 0; checked < len(order); checked++ {
			index := (*cursor + checked) % len(order)
			u := order[index]
			for len(u.queue) > 0 && now.Sub(u.queue[0].at) > multiQueueAge {
				u.queue[0] = packet{}
				u.queue = u.queue[1:]
				stats.QueueExpired.Add(1)
			}
			if len(u.queue) > 0 {
				selected = u
				selectedIndex = index
				break
			}
		}
		if selected == nil {
			break
		}
		if !budget.allow(now) {
			pending = true
			break
		}
		*cursor = (selectedIndex + 1) % len(order)
		p := selected.queue[0]
		selected.queue[0] = packet{}
		selected.queue = selected.queue[1:]
		select {
		case selected.socket.incoming <- p:
			stats.Dispatched.Add(1)
		default:
			stats.QueueDropped.Add(1)
		}
	}
	if !pending {
		for _, u := range order {
			if len(u.queue) > 0 {
				pending = true
				break
			}
		}
	}
	return pending
}
