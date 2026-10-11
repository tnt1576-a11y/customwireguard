package transport

import (
	"encoding/binary"
	"sync"
	"time"
)

// Shared scheduling is explicitly negotiated inside authenticated bootstrap
// packets. Four encrypted request bytes carry the acknowledged grant version,
// desired rate and configured maximum. Three extra reply bytes carry that
// version and granted rate.
// A rate unit is 100 requests/s. Even a maximum native payload still fits in
// the existing 1400-byte minimum outer datagram; no visible header is added.
var sharedCapability = []byte("cwg-share1")

const sharedMinimum = 2
const sharedMaximum = 120
const sharedClientLease = time.Second
const sharedServerLease = 2 * time.Second

type sharedSocket interface {
	sharedGrant(time.Time, uint16, int, bool) (uint16, int)
}

func sharedUnits(rate int) int { return min(sharedMaximum, max(sharedMinimum, (rate+99)/100)) }

type clientGrant struct {
	version uint16
	units   int
	at      time.Time
}

func (g *clientGrant) accept(version uint16, units int, sentAt time.Time) {
	*g = clientGrant{version: version, units: units, at: sentAt}
}

func (g *clientGrant) limit(now time.Time) int {
	if g.units < sharedMinimum || now.Sub(g.at) > sharedClientLease {
		return sharedMinimum * 100
	}
	return g.units * 100
}

func sharedRequest(payload []byte, grant uint16, desired, ceiling int) []byte {
	return sharedRequestInto(make([]byte, 4+len(payload)), payload, grant, desired, ceiling)
}

// The adaptive client's scratch header is consumed synchronously by SealInto.
func sharedRequestInto(dst, payload []byte, grant uint16, desired, ceiling int) []byte {
	p := dst[:4+len(payload)]
	binary.BigEndian.PutUint16(p, grant)
	p[2] = byte(sharedUnits(desired))
	p[3] = byte(sharedUnits(ceiling))
	copy(p[4:], payload)
	return p
}

type rateLease struct {
	version                  uint16
	target, reserved, demand int
	seen                     time.Time
}

// A reduction does not make its capacity available to someone else until the
// client acknowledges that exact advertised version. The client expires an
// unanswered grant before the server releases a silent client's reservation.
// Three minimum slots are always reserved, including accounts not yet created,
// so adding an account cannot over-allocate an existing busy client's grant.
type sharedScheduler struct {
	mu      sync.Mutex
	ceiling int
	last    time.Time
	leases  map[*rateLease]bool
	order   []*rateLease
}

func newSharedScheduler(pps int) *sharedScheduler {
	return &sharedScheduler{ceiling: sharedCapacity(pps), leases: make(map[*rateLease]bool)}
}

// Leave ten percent below the hard ingress ceiling for packet compression and
// scheduling jitter. Filling the hard ceiling continuously makes even a small
// arrival burst persist until the short ingress queue deadline.
func sharedCapacity(pps int) int { return max(3*sharedMinimum, pps*9/1000) }
func (s *sharedScheduler) add() *rateLease {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := &rateLease{version: 1, target: sharedMinimum, reserved: sharedMinimum, demand: sharedMinimum}
	s.leases[l] = true
	s.order = append(s.order, l)
	return l
}
func (s *sharedScheduler) remove(l *rateLease) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.leases, l)
	for i, current := range s.order {
		if current == l {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
}
func (s *sharedScheduler) configure(pps int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ceiling = sharedCapacity(pps)
	// Recompute at the next authenticated request; outstanding reservations
	// are still retained until the corresponding reductions are acknowledged.
	s.last = time.Time{}
}
func (s *sharedScheduler) observe(l *rateLease, now time.Time, ack uint16, wanted int, newest bool) (uint16, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if newest {
		l.seen = now
		l.demand = min(sharedMaximum, max(sharedMinimum, wanted))
		if ack == l.version && l.target < l.reserved {
			l.reserved = l.target
		}
	}
	// 20 updates/s bounds feedback work and makes 16-bit version wrap much
	// longer than the two-second grant lifetime and authenticated replay window.
	if s.last.IsZero() || now.Sub(s.last) >= 50*time.Millisecond {
		s.allocate(now)
		s.last = now
	}
	return l.version, l.target
}
func setLease(l *rateLease, units int) {
	if l.target == units {
		return
	}
	l.target = units
	l.version++
	if l.version == 0 {
		l.version = 1
	}
}
func (s *sharedScheduler) allocate(now time.Time) {
	all := make([]*rateLease, 0, len(s.leases))
	wanted := make(map[*rateLease]int, len(s.leases))
	available := s.ceiling - (3-len(s.leases))*sharedMinimum
	for _, l := range s.order {
		if l.seen.IsZero() || now.Sub(l.seen) > sharedServerLease {
			// A compliant silent client has already reverted to the minimum.
			setLease(l, sharedMinimum)
			l.reserved, l.demand = sharedMinimum, sharedMinimum
		}
		all = append(all, l)
		wanted[l] = sharedMinimum
		available -= sharedMinimum
	}
	// Max-min allocation. Stop at each client's declared demand; idle users
	// leave almost all of their unused share available to a busy user.
	for available > 0 {
		progress := false
		for _, l := range all {
			if wanted[l] < l.demand && available > 0 {
				wanted[l]++
				available--
				progress = true
			}
		}
		if !progress {
			break
		}
	}
	free := s.ceiling - (3-len(s.leases))*sharedMinimum
	for _, l := range all {
		if wanted[l] < l.target {
			setLease(l, wanted[l])
		}
		free -= l.reserved
	}
	// Do not revoke an unacknowledged reduction by advertising an increase:
	// only reservations already freed by acknowledgments can be lent out.
	growing := make(map[*rateLease]int, len(all))
	for _, l := range all {
		if l.target == l.reserved {
			growing[l] = l.target
		}
	}
	for free > 0 {
		progress := false
		for _, l := range all {
			if units, ok := growing[l]; ok && units < wanted[l] && free > 0 {
				growing[l]++
				free--
				progress = true
			}
		}
		if !progress {
			break
		}
	}
	for l, units := range growing {
		l.reserved = units
		setLease(l, units)
	}
}
