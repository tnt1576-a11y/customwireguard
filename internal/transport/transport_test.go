package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"customwireguard/internal/config"
	"customwireguard/internal/wire"
)

func socket(t testing.TB) *net.UDPConn {
	t.Helper()
	c, e := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func keyString() string {
	key := make([]byte, 32)
	rand.Read(key)
	return base64.StdEncoding.EncodeToString(key)
}

type fixture struct {
	server, backend *net.UDPConn
	cfg             config.Config
	stats           *Stats
	cancel          context.CancelFunc
}

func serverFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{server: socket(t), backend: socket(t), stats: new(Stats)}
	f.cfg = config.Config{Mode: "server", Listen: f.server.LocalAddr().String(), WireGuard: f.backend.LocalAddr().String(), Key: keyString(), Profile: "balanced"}
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	done := make(chan error, 1)
	go func() { done <- RunServer(ctx, f.server, f.cfg, f.stats) }()
	t.Cleanup(func() {
		cancel()
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(3 * time.Second):
			t.Error("server did not stop")
		}
	})
	return f
}

func handshake(t *testing.T, f *fixture) (*net.UDPConn, *association, *wire.Codec, []byte) {
	t.Helper()
	conn, e := net.DialUDP("udp4", nil, f.server.LocalAddr().(*net.UDPAddr))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { conn.Close() })
	key, _ := f.cfg.Secret()
	boot, _ := wire.NewCodec(key, nil, true)
	id := wire.NewID()
	hello, _ := boot.Seal(wire.Frame{Kind: wire.Hello, ID: id, Timestamp: time.Now().Unix()}, 320)
	conn.Write(hello)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 2000)
	n, e := conn.Read(buf)
	if e != nil {
		t.Fatal(e)
	}
	welcome, e := boot.Open(buf[:n], time.Now())
	if e != nil {
		t.Fatal(e)
	}
	a, e := newAssociation(key, id, welcome.Payload, true, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	return conn, a, boot, hello
}

func TestUnauthenticatedSilence(t *testing.T) {
	f := serverFixture(t)
	attacker := socket(t)
	for _, size := range []int{0, 4, 148, 256, 1440, 65000} {
		bad := make([]byte, size)
		rand.Read(bad)
		attacker.WriteToUDP(bad, f.server.LocalAddr().(*net.UDPAddr))
	}
	attacker.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	buf := make([]byte, 65535)
	if _, _, e := attacker.ReadFromUDP(buf); e == nil {
		t.Fatal("unauthenticated packet elicited reply")
	}
	f.backend.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if _, _, e := f.backend.ReadFromUDP(buf); e == nil {
		t.Fatal("unauthenticated packet reached WireGuard")
	}
}

func TestReplayAndBootstrapSourceBinding(t *testing.T) {
	f := serverFixture(t)
	conn, a, _, hello := handshake(t, f)
	data, _ := a.seal(wire.Data, []byte("once"), "balanced", time.Now())
	conn.Write(data)
	conn.Write(data)
	f.backend.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 2000)
	n, _, e := f.backend.ReadFromUDP(buf)
	if e != nil || string(buf[:n]) != "once" {
		t.Fatalf("forward: %v", e)
	}
	f.backend.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if _, _, e = f.backend.ReadFromUDP(buf); e == nil {
		t.Fatal("replayed data forwarded")
	}
	other := socket(t)
	other.WriteToUDP(hello, f.server.LocalAddr().(*net.UDPAddr))
	other.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	if _, _, e = other.ReadFromUDP(buf); e == nil {
		t.Fatal("captured bootstrap replay answered at another address")
	}
}

func TestDatagramRoundtripAndProbeDoesNotReplaceClient(t *testing.T) {
	for _, profile := range []string{"minimal", "balanced"} {
		t.Run(profile, func(t *testing.T) {
			f := serverFixture(t)
			wgLocal := socket(t)
			local := socket(t)
			remote, e := net.DialUDP("udp4", nil, f.server.LocalAddr().(*net.UDPAddr))
			if e != nil {
				t.Fatal(e)
			}
			cfg := config.Config{Mode: "client", Listen: local.LocalAddr().String(), Endpoint: f.server.LocalAddr().String(), WireGuard: wgLocal.LocalAddr().String(), Key: f.cfg.Key, Profile: profile}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			stats := new(Stats)
			go func() { done <- RunClient(ctx, local, remote, cfg, stats) }()
			defer func() {
				cancel()
				select {
				case e := <-done:
					if e != nil {
						t.Error(e)
					}
				case <-time.After(3 * time.Second):
					t.Error("client did not stop")
				}
			}()
			for _, size := range []int{32, 148, 1280, wire.MaxPayload} {
				payload := make([]byte, size)
				rand.Read(payload)
				wgLocal.WriteToUDP(payload, local.LocalAddr().(*net.UDPAddr))
				buf := make([]byte, 2000)
				f.backend.SetReadDeadline(time.Now().Add(3 * time.Second))
				n, addr, e := f.backend.ReadFromUDP(buf)
				if e != nil || !bytes.Equal(payload, buf[:n]) {
					t.Fatalf("outgoing %d: %v", size, e)
				}
				f.backend.WriteToUDP(buf[:n], addr)
				wgLocal.SetReadDeadline(time.Now().Add(2 * time.Second))
				n, _, e = wgLocal.ReadFromUDP(buf)
				if e != nil || !bytes.Equal(payload, buf[:n]) {
					t.Fatalf("incoming %d: %v", size, e)
				}
			}
			before := f.stats.Established.Load()
			result, e := ProbeEndpoint(ctx, cfg, 1)
			if e != nil || result.Received != 1 {
				t.Fatalf("probe: %+v %v", result, e)
			}
			if f.stats.Established.Load() != before {
				t.Fatal("probe replaced live session")
			}
		})
	}
}

func TestAuthenticatedRebindingIgnoresOlderSource(t *testing.T) {
	f := serverFixture(t)
	first, a, _, _ := handshake(t, f)
	old, _ := a.seal(wire.Data, []byte("old"), "balanced", time.Now())
	newer, _ := a.seal(wire.Data, []byte("new"), "balanced", time.Now())
	second := socket(t)
	second.WriteToUDP(newer, f.server.LocalAddr().(*net.UDPAddr))
	buf := make([]byte, 2000)
	f.backend.SetReadDeadline(time.Now().Add(time.Second))
	_, backendPeer, e := f.backend.ReadFromUDP(buf)
	if e != nil {
		t.Fatal(e)
	}
	first.Write(old)
	f.backend.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, e = f.backend.ReadFromUDP(buf); e != nil {
		t.Fatal(e)
	}
	f.backend.WriteToUDP([]byte("response"), backendPeer)
	second.SetReadDeadline(time.Now().Add(time.Second))
	n, _, e := second.ReadFromUDP(buf)
	if e != nil {
		t.Fatal(e)
	}
	frame, ok, _ := a.open(buf[:n], time.Now())
	if !ok || string(frame.Payload) != "response" {
		t.Fatal("response not delivered to newest authenticated source")
	}
}

func TestClientRetriesLostWelcome(t *testing.T) {
	f := serverFixture(t)
	relay, local, wgLocal := socket(t), socket(t), socket(t)
	toServer, e := net.DialUDP("udp4", nil, f.server.LocalAddr().(*net.UDPAddr))
	if e != nil {
		t.Fatal(e)
	}
	remote, e := net.DialUDP("udp4", nil, relay.LocalAddr().(*net.UDPAddr))
	if e != nil {
		t.Fatal(e)
	}
	var mu sync.Mutex
	var clientAddr *net.UDPAddr
	var replies atomic.Uint64
	var relayWG sync.WaitGroup
	relayWG.Add(2)
	go func() {
		defer relayWG.Done()
		buf := make([]byte, 2000)
		for {
			n, addr, e := relay.ReadFromUDP(buf)
			if e != nil {
				return
			}
			mu.Lock()
			clientAddr = addr
			mu.Unlock()
			toServer.Write(buf[:n])
		}
	}()
	go func() {
		defer relayWG.Done()
		buf := make([]byte, 2000)
		for {
			n, e := toServer.Read(buf)
			if e != nil {
				return
			}
			if replies.Add(1) == 1 {
				continue
			}
			mu.Lock()
			addr := clientAddr
			mu.Unlock()
			if addr != nil {
				relay.WriteToUDP(buf[:n], addr)
			}
		}
	}()
	defer func() { relay.Close(); toServer.Close(); relayWG.Wait() }()
	cfg := config.Config{Mode: "client", Listen: local.LocalAddr().String(), Endpoint: relay.LocalAddr().String(), WireGuard: wgLocal.LocalAddr().String(), Key: f.cfg.Key, Profile: "balanced"}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunClient(ctx, local, remote, cfg, new(Stats)) }()
	defer func() {
		cancel()
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(3 * time.Second):
			t.Error("client shutdown timeout")
		}
	}()
	wgLocal.WriteToUDP([]byte("survives one lost welcome"), local.LocalAddr().(*net.UDPAddr))
	f.backend.SetReadDeadline(time.Now().Add(6 * time.Second))
	buf := make([]byte, 2000)
	n, _, e := f.backend.ReadFromUDP(buf)
	if e != nil || string(buf[:n]) != "survives one lost welcome" || replies.Load() < 2 {
		t.Fatalf("retry recovery failed: %v", e)
	}
}
