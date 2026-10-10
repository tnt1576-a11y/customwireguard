package transport

import (
	"bytes"
	"encoding/binary"
	"time"
)

// FEC is inside authenticated exchange payloads, never an exposed UDP header.
// Four interleaved groups each hold up to four native data packets and one XOR
// parity packet. Originals pass immediately. One loss per group is recoverable;
// two losses in the same group are not guessed or retransmitted.
const (
	fecMagic         = 0x43465831 // CFX1, incompatible with native WireGuard message types
	fecHeader        = 16
	fecDataLimit     = 1280 // parity: 16-byte header + 8 lengths + 1280 <= 1312
	fecGroupSize     = 4
	fecLanes         = 4
	fecReceiveGroups = 128
)

var fecCapability = []byte("cwg-fec1")

func fecBootstrap(enabled bool) []byte {
	if enabled {
		return fecCapability
	}
	return nil
}

func nativeFECData(p []byte) bool {
	return len(p) >= 32 && binary.LittleEndian.Uint32(p[:4]) == 4
}

func fecFrame(id uint64, kind, index byte, body []byte) []byte {
	p := make([]byte, fecHeader+len(body))
	binary.BigEndian.PutUint32(p, fecMagic)
	binary.BigEndian.PutUint64(p[4:], id)
	p[12], p[13] = kind, index
	binary.BigEndian.PutUint16(p[14:], uint16(len(body)))
	copy(p[fecHeader:], body)
	return p
}

type fecSendGroup struct {
	id      uint64
	at      time.Time
	count   int
	lengths [fecGroupSize]uint16
	xor     []byte
}

type fecSender struct {
	lanes                        [fecLanes]fecSendGroup
	parity                       [16]exchangeItem
	head, count, lane, originals int
	nextID                       uint64
	stats                        *Stats
}

func (s *fecSender) finish(index int) {
	g := &s.lanes[index]
	if g.count == 0 {
		return
	}
	body := make([]byte, 2*fecGroupSize+len(g.xor))
	for i, n := range g.lengths {
		binary.BigEndian.PutUint16(body[i*2:], n)
	}
	copy(body[2*fecGroupSize:], g.xor)
	if s.count == len(s.parity) {
		s.parity[s.head] = exchangeItem{}
		s.head = (s.head + 1) % len(s.parity)
		s.count--
		s.stats.ExchangeFECExpired.Add(1)
	}
	s.parity[(s.head+s.count)%len(s.parity)] = exchangeItem{fecFrame(g.id, 1, byte(g.count), body), g.at}
	s.count++
	*g = fecSendGroup{}
}

func (s *fecSender) flush(now time.Time) {
	for i := range s.lanes {
		if s.lanes[i].count > 0 && now.Sub(s.lanes[i].at) >= 8*time.Millisecond {
			s.finish(i)
		}
	}
	for s.count > 0 && now.Sub(s.parity[s.head].at) >= 50*time.Millisecond {
		s.parity[s.head] = exchangeItem{}
		s.head = (s.head + 1) % len(s.parity)
		s.count--
		s.stats.ExchangeFECExpired.Add(1)
	}
}

func (s *fecSender) push(data []byte, now time.Time) []byte {
	if !nativeFECData(data) {
		return data
	}
	if len(data) > fecDataLimit || s.nextID == ^uint64(0) {
		s.stats.ExchangeFECSkipped.Add(1)
		return data // large native packets retain the legacy MTU instead of dropping
	}
	s.flush(now)
	i := s.lane
	s.lane = (s.lane + 1) % fecLanes
	g := &s.lanes[i]
	if g.count == 0 {
		s.nextID++
		g.id, g.at = s.nextID, now
	}
	index := g.count
	g.count++
	g.lengths[index] = uint16(len(data))
	if len(data) > len(g.xor) {
		g.xor = append(g.xor, make([]byte, len(data)-len(g.xor))...)
	}
	for n, b := range data {
		g.xor[n] ^= b
	}
	out := fecFrame(g.id, 0, byte(index), data)
	if g.count == fecGroupSize {
		s.finish(i)
	}
	return out
}

func (s *fecSender) popParity(now time.Time) []byte {
	s.flush(now)
	if s.count == 0 {
		return nil
	}
	p := s.parity[s.head].data
	s.parity[s.head] = exchangeItem{}
	s.head = (s.head + 1) % len(s.parity)
	s.count--
	s.originals = 0
	return p
}

// Parity shares the existing request ceiling; no extra outer packets or timer.
// Small originals can pass at most four times while parity is ready. This keeps
// recovery available under bulk load without unbounded priority inversion.
func (s *fecSender) next(q *exchangeQueue, now time.Time) (payload []byte, parity bool) {
	q.expire(now)
	s.flush(now)
	urgentSmall := len(q.small) > 0 && now.Sub(q.small[0].at) >= q.maxAge/2
	if s.count > 0 && (len(q.small) == 0 || (s.originals >= 4 && !urgentSmall)) {
		return s.popParity(now), true
	}
	p := q.pop(now)
	if len(p) == 0 {
		p = s.popParity(now)
		return p, len(p) > 0
	}
	s.originals++
	return s.push(p, now), false
}

func (s *fecSender) reset() { *s = fecSender{stats: s.stats} }

type fecReceiveGroup struct {
	id        uint64
	at        time.Time
	packets   [fecGroupSize][]byte
	parity    []byte
	lengths   [fecGroupSize]uint16
	count     int
	recovered bool
}

type fecReceiver struct {
	groups [fecReceiveGroups]fecReceiveGroup
	newest uint64
	stats  *Stats
	emit   func([]byte)
}

func (r *fecReceiver) reset() { *r = fecReceiver{stats: r.stats, emit: r.emit} }

func (r *fecReceiver) invalid() { r.stats.ExchangeFECInvalid.Add(1) }

func (r *fecReceiver) push(p []byte, now time.Time) {
	if len(p) == 0 {
		return
	}
	if len(p) < 4 || binary.BigEndian.Uint32(p) != fecMagic {
		r.emit(p) // native handshakes, keepalives and larger MTU packets
		return
	}
	if len(p) < fecHeader || int(binary.BigEndian.Uint16(p[14:])) != len(p)-fecHeader {
		r.invalid()
		return
	}
	id, kind, index := binary.BigEndian.Uint64(p[4:]), p[12], int(p[13])
	body := p[fecHeader:]
	if id == 0 || kind > 1 {
		r.invalid()
		return
	}
	var lengths [fecGroupSize]uint16
	if kind == 0 {
		if index >= fecGroupSize || !nativeFECData(body) || len(body) > fecDataLimit {
			r.invalid()
			return
		}
	} else {
		if index < 1 || index > fecGroupSize || len(body) < 2*fecGroupSize+32 || len(body) > 2*fecGroupSize+fecDataLimit {
			r.invalid()
			return
		}
		maximum := 0
		for i := range lengths {
			lengths[i] = binary.BigEndian.Uint16(body[2*i:])
			n := int(lengths[i])
			if (i < index && (n < 32 || n > fecDataLimit)) || (i >= index && n != 0) {
				r.invalid()
				return
			}
			maximum = max(maximum, n)
		}
		if maximum != len(body)-2*fecGroupSize {
			r.invalid()
			return
		}
	}
	if id > r.newest {
		r.newest = id
	}
	if r.newest-id >= fecReceiveGroups {
		r.stats.ExchangeFECLate.Add(1)
		if kind == 0 {
			r.emit(body)
		}
		return
	}
	g := &r.groups[id%fecReceiveGroups]
	if g.id != id || now.Sub(g.at) >= 250*time.Millisecond {
		*g = fecReceiveGroup{id: id, at: now}
	}
	if kind == 0 {
		if (g.count != 0 && (index >= g.count || len(body) != int(g.lengths[index]))) || (g.packets[index] != nil && !bytes.Equal(g.packets[index], body)) {
			r.invalid()
			return
		}
		if g.packets[index] != nil {
			return
		} // a late original may already have been reconstructed
		g.packets[index] = body
		r.emit(body)
	} else {
		if g.count != 0 && (g.count != index || g.lengths != lengths || !bytes.Equal(g.parity, body[2*fecGroupSize:])) {
			r.invalid()
			return
		}
		for i, data := range g.packets {
			if data != nil && (i >= index || len(data) != int(lengths[i])) {
				r.invalid()
				return
			}
		}
		g.count, g.lengths, g.parity = index, lengths, body[2*fecGroupSize:]
	}
	if g.count == 0 || g.recovered {
		return
	}
	missing := -1
	for i := 0; i < g.count; i++ {
		if g.packets[i] == nil {
			if missing != -1 {
				return
			}
			missing = i
		}
	}
	if missing == -1 {
		return
	}
	recovered := append([]byte(nil), g.parity...)
	for _, data := range g.packets {
		for i, b := range data {
			recovered[i] ^= b
		}
	}
	recovered = recovered[:g.lengths[missing]]
	if !nativeFECData(recovered) {
		r.invalid()
		return
	}
	g.packets[missing], g.recovered = recovered, true
	r.stats.ExchangeFECRecovered.Add(1)
	r.emit(recovered) // native WireGuard still performs AEAD and replay checks
}
