package transport

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"net"
	"testing"
	"time"

	"customwireguard/internal/config"
	"customwireguard/internal/wire"
)

type multiClient struct {
	conn  *net.UDPConn
	a     *association
	grant uint16
}

func openMultiClient(t *testing.T, address *net.UDPAddr, cfg config.Config, existing *net.UDPConn) *multiClient {
	t.Helper()
	conn := existing
	if conn == nil {
		var err error
		conn, err = net.DialUDP("udp4", nil, address)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
	}
	key, _ := cfg.Secret()
	boot, _ := wire.NewExchangeCodec(key, nil, true)
	id := wire.NewID()
	hello, _ := boot.Seal(wire.Frame{Kind: wire.Hello, ID: id, Timestamp: time.Now().Unix(), Payload: sharedCapability}, 320)
	conn.Write(hello)
	conn.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 2048)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal("multi-user welcome", err)
	}
	welcome, err := boot.Open(buf[:n], time.Now())
	if err != nil || welcome.Kind != wire.Welcome || len(welcome.Payload) != 33 || welcome.Payload[32] != 1 {
		t.Fatal("invalid welcome", err)
	}
	a, err := newAssociationMode(key, id, welcome.Payload[:32], true, time.Now(), true)
	if err != nil {
		t.Fatal(err)
	}
	a.shared = true
	return &multiClient{conn: conn, a: a}
}
func (c *multiClient) exchange(t *testing.T, data []byte) []byte {
	t.Helper()
	packet, err := c.a.sealSized(wire.Exchange, sharedRequest(data, c.grant, 200, 8000), 1407, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	sequence := c.a.send
	if _, err = c.conn.Write(packet); err != nil {
		t.Fatal(err)
	}
	c.conn.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 2048)
	n, err := c.conn.Read(buf)
	if err != nil {
		t.Fatal("multi-user exchange", err)
	}
	frame, ok, _ := c.a.open(buf[:n], time.Now())
	if !ok || frame.Kind != wire.ExchangeReply || len(frame.Payload) < 12 || binary.BigEndian.Uint64(frame.Payload[:8]) != sequence {
		t.Fatal("wrong user's response or sequence")
	}
	c.grant = binary.BigEndian.Uint16(frame.Payload[9:11])
	return frame.Payload[12:]
}
func waitMulti(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("multi-user state transition timed out")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestMultiFairDispatchAndAgeLimit(t *testing.T) {
	now := time.Unix(1700000000, 0)
	users := make([]*multiUser, 3)
	for i := range users {
		users[i] = &multiUser{socket: &userSocket{incoming: make(chan packet, 128)}}
		for j := 0; j < 64; j++ {
			users[i].queue = append(users[i].queue, packet{at: now})
		}
	}
	stats, cursor := new(MultiStats), 0
	// Eight tokens per millisecond is deliberately not divisible by three.
	budget := bucket{rate: 8000, burst: 8}
	for step := 0; step < 3; step++ {
		if !dispatchMulti(users, &cursor, &budget, stats, now.Add(time.Duration(step)*time.Millisecond)) {
			t.Fatal("saturated queues unexpectedly drained")
		}
		if stats.Dispatched.Load() != uint64((step+1)*8) {
			t.Fatal("aggregate budget exceeded or unused")
		}
	}
	for _, u := range users {
		if len(u.socket.incoming) != 8 {
			t.Fatal("a saturated user was starved", len(u.socket.incoming))
		}
	}
	if dispatchMulti(users, &cursor, &budget, stats, now.Add(6*time.Millisecond)) {
		t.Fatal("expired queues retained")
	}
	if stats.QueueExpired.Load() != 168 || stats.Dispatched.Load() != 24 {
		t.Fatal("original receive age was not enforced")
	}
	// A sole active user can borrow all available capacity.
	for j := 0; j < 8; j++ {
		users[1].queue = append(users[1].queue, packet{at: now.Add(7 * time.Millisecond)})
	}
	if dispatchMulti(users, &cursor, &budget, stats, now.Add(7*time.Millisecond)) || stats.Dispatched.Load() != 32 {
		t.Fatal("unused share was not borrowed")
	}
}

func TestMultiWorkerBackpressurePreservesYoungPacket(t *testing.T) {
	now := time.Unix(1700000000, 0)
	a := &multiUser{socket: &userSocket{incoming: make(chan packet, 1)}}
	b := &multiUser{socket: &userSocket{incoming: make(chan packet, 1)}}
	a.socket.incoming <- packet{data: []byte("already dispatched"), at: now}
	a.queue = []packet{{data: []byte("held"), at: now}}
	b.queue = []packet{{data: []byte("other user"), at: now}}
	users := []*multiUser{a, b}
	stats, cursor := new(MultiStats), 0
	budget := bucket{rate: 8000, burst: 2}
	if !dispatchMulti(users, &cursor, &budget, stats, now) {
		t.Fatal("blocked worker lost its pending packet")
	}
	if len(a.queue) != 1 || len(b.queue) != 0 || stats.Dispatched.Load() != 1 || budget.tokens != 1 || stats.QueueDropped.Load() != 0 {
		t.Fatal("worker backpressure dropped data, wasted tokens or blocked another user")
	}
	<-a.socket.incoming
	if dispatchMulti(users, &cursor, &budget, stats, now) || string((<-a.socket.incoming).data) != "held" {
		t.Fatal("young packet not delivered after worker recovered")
	}
	// Keeping a blocked packet must not refresh its original deadline.
	a.socket.incoming <- packet{at: now}
	a.queue = []packet{{data: []byte("expires"), at: now}}
	if dispatchMulti(users, &cursor, &budget, stats, now.Add(6*time.Millisecond)) || stats.QueueExpired.Load() != 1 {
		t.Fatal("blocked packet outlived the short queue deadline")
	}
}

func TestMultiCompressedArrivalKeepsBoundedBudget(t *testing.T) {
	now := time.Unix(1700000000, 0)
	users := make([]*multiUser, 3)
	for i := range users {
		users[i] = &multiUser{socket: &userSocket{incoming: make(chan packet, multiQueueCapacity)}}
		for j := 0; j < 40; j++ {
			users[i].queue = append(users[i].queue, packet{at: now})
		}
	}
	stats, cursor, budget := new(MultiStats), 0, multiBudget(8000)
	dispatchMulti(users, &cursor, &budget, stats, now)
	if dispatchMulti(users, &cursor, &budget, stats, now) || stats.Dispatched.Load() != 120 || stats.QueueDropped.Load() != 0 {
		t.Fatal("paced requests compressed into a short batch were needlessly queued or dropped")
	}
	// The burst allowance is finite, not a new continuous request rate.
	for i := 0; i < 8; i++ {
		if !budget.allow(now) {
			t.Fatal("incorrect finite burst allowance")
		}
	}
	if budget.allow(now) || !budget.allow(now.Add(125*time.Microsecond)) || budget.allow(now.Add(125*time.Microsecond)) {
		t.Fatal("hard dispatch rate was relaxed after the bounded burst")
	}
}

func TestMultiIsolationRoamingAndRevocation(t *testing.T) {
	public := socket(t)
	backends := []*net.UDPConn{socket(t), socket(t), socket(t)}
	cfg := config.MultiServer{Mode: "server-multi", Listen: public.LocalAddr().String(), MaxPPS: 8000}
	for i, name := range []string{"alice", "bob", "charlie"} {
		key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{byte(i + 1)}, 32))
		cfg.Users = append(cfg.Users, config.ServerUser{Name: name, Config: config.Config{Mode: "server", Transport: "exchange", ExchangeShared: true, Listen: cfg.Listen, WireGuard: backends[i].LocalAddr().String(), Key: key, Profile: "balanced"}})
	}
	stats := new(MultiStats)
	updates := make(chan config.MultiServer)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunMultiServer(ctx, public, cfg, updates, stats) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("multi-user shutdown deadlocked")
		}
	}()
	rogue, err := net.DialUDP("udp4", nil, public.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer rogue.Close()
	rogue.Write(bytes.Repeat([]byte{0x7b}, 1400))
	rogue.SetReadDeadline(time.Now().Add(30 * time.Millisecond))
	if _, err = rogue.Read(make([]byte, 2048)); err == nil {
		t.Fatal("reply to unauthenticated input")
	}
	if stats.Routes.Load() != 0 {
		t.Fatal("unauthenticated input learned an address")
	}
	clients := make([]*multiClient, 3)
	peers := make([]*net.UDPAddr, 3)
	buf := make([]byte, 2048)
	for i, u := range cfg.Users {
		clients[i] = openMultiClient(t, public.LocalAddr().(*net.UDPAddr), u.Config, nil)
		clients[i].exchange(t, []byte(u.Name))
		backends[i].SetReadDeadline(time.Now().Add(time.Second))
		n, peer, e := backends[i].ReadFromUDP(buf)
		if e != nil || string(buf[:n]) != u.Name {
			t.Fatal("backend isolation failed", e)
		}
		peers[i] = peer
	}
	for i, u := range cfg.Users {
		backends[i].WriteToUDP([]byte("only-"+u.Name), peers[i])
	}
	for i, u := range cfg.Users {
		deadline := time.Now().Add(time.Second)
		for {
			reply := clients[i].exchange(t, nil)
			if len(reply) > 0 {
				if string(reply) != "only-"+u.Name {
					t.Fatal("cross-user downstream delivery")
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("downstream not delivered")
			}
			time.Sleep(time.Millisecond)
		}
	}
	// Keep the cryptographic session while moving to a new UDP source address.
	newAddress, e := net.DialUDP("udp4", nil, public.LocalAddr().(*net.UDPAddr))
	if e != nil {
		t.Fatal(e)
	}
	defer newAddress.Close()
	clients[0].conn = newAddress
	clients[0].exchange(t, nil)
	current := cfg
	current.Users = []config.ServerUser{cfg.Users[0], cfg.Users[2]}
	updates <- current
	waitMulti(t, func() bool { stats.mu.RLock(); defer stats.mu.RUnlock(); return len(stats.users) == 2 })
	clients[0].exchange(t, nil)
	clients[2].exchange(t, nil)
	for _, i := range []int{0, 2} {
		stats.mu.RLock()
		established := stats.users[cfg.Users[i].Name].Established.Load()
		stats.mu.RUnlock()
		if established != 1 {
			t.Fatal("reload reset an unaffected session")
		}
	}
	old, _ := clients[1].a.sealSized(wire.Exchange, sharedRequest([]byte("revoked"), clients[1].grant, 200, 8000), 1407, time.Now())
	clients[1].conn.Write(old)
	clients[1].conn.SetReadDeadline(time.Now().Add(40 * time.Millisecond))
	if _, e = clients[1].conn.Read(buf); e == nil {
		t.Fatal("revoked user received a response")
	}
	bad := current
	bad.Users = append([]config.ServerUser(nil), current.Users...)
	bad.Users[1].Config.Key = bad.Users[0].Config.Key
	updates <- bad
	waitMulti(t, func() bool { return stats.ReloadRejected.Load() == 1 })
	clients[0].exchange(t, nil)
	// A new credential can occupy a source address previously cached for another
	// user. Its short authenticated Hello must bypass the stale address hint.
	updates <- cfg
	waitMulti(t, func() bool { stats.mu.RLock(); defer stats.mu.RUnlock(); return len(stats.users) == 3 })
	reused := openMultiClient(t, public.LocalAddr().(*net.UDPAddr), cfg.Users[1].Config, clients[0].conn)
	reused.exchange(t, []byte("bob-reused-port"))
	backends[1].SetReadDeadline(time.Now().Add(time.Second))
	n, _, e := backends[1].ReadFromUDP(buf)
	if e != nil || string(buf[:n]) != "bob-reused-port" {
		t.Fatal("source-port reuse misrouted", e)
	}
	empty := cfg
	empty.Users = nil
	updates <- empty
	waitMulti(t, func() bool { return stats.Routes.Load() == 0 })
}
