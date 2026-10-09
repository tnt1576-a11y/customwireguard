package transport

import (
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"

	"customwireguard/internal/config"
	"customwireguard/internal/wire"
)

func TestExchangeResponseOnlyAndReplay(t *testing.T) {
	public, backend := socket(t), socket(t)
	cfg := config.Config{Mode: "server", Transport: "exchange", Listen: public.LocalAddr().String(), WireGuard: backend.LocalAddr().String(), Key: keyString(), Profile: "balanced"}
	stats := new(Stats)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunServer(ctx, public, cfg, stats) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("server did not stop")
		}
	}()
	conn, err := net.DialUDP("udp4", nil, public.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	key, _ := cfg.Secret()
	boot, _ := wire.NewExchangeCodec(key, nil, true)
	id := wire.NewID()
	hello, _ := boot.Seal(wire.Frame{Kind: wire.Hello, ID: id, Timestamp: time.Now().Unix()}, 320)
	conn.Write(hello)
	conn.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 2048)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	welcome, err := boot.Open(buf[:n], time.Now())
	if err != nil {
		t.Fatal(err)
	}
	a, err := newAssociationMode(key, id, welcome.Payload, true, time.Now(), true)
	if err != nil {
		t.Fatal(err)
	}
	request, _ := a.sealSized(wire.Exchange, []byte("up"), 1407, time.Now())
	seq := a.send
	conn.Write(request)
	backend.SetReadDeadline(time.Now().Add(time.Second))
	n, peer, err := backend.ReadFromUDP(buf)
	if err != nil || string(buf[:n]) != "up" {
		t.Fatal("upload", err)
	}
	n, err = conn.Read(buf)
	if err != nil || n != 1407 {
		t.Fatal("reply size", n, err)
	}
	f, ok, _ := a.open(buf[:n], time.Now())
	if !ok || f.Kind != wire.ExchangeReply || binary.BigEndian.Uint64(f.Payload[:8]) != seq || len(f.Payload) != 9 {
		t.Fatal("invalid correlated empty reply")
	}
	backend.WriteToUDP([]byte("down"), peer)
	conn.SetReadDeadline(time.Now().Add(60 * time.Millisecond))
	if _, err = conn.Read(buf); err == nil {
		t.Fatal("unsolicited server packet")
	}
	// Replayed and unauthenticated requests must elicit nothing and must not
	// cause duplicate forwarding into the native backend.
	conn.Write(request)
	conn.Write(bytes.Repeat([]byte{123}, 1400))
	conn.SetReadDeadline(time.Now().Add(60 * time.Millisecond))
	if _, err = conn.Read(buf); err == nil {
		t.Fatal("reply to replay or unauthenticated packet")
	}
	request, _ = a.sealSized(wire.Exchange, nil, 1423, time.Now())
	seq = a.send
	conn.Write(request)
	conn.SetReadDeadline(time.Now().Add(time.Second))
	n, err = conn.Read(buf)
	if err != nil || n != 1423 {
		t.Fatal("queued reply", n, err)
	}
	f, ok, _ = a.open(buf[:n], time.Now())
	if !ok || binary.BigEndian.Uint64(f.Payload[:8]) != seq || string(f.Payload[9:]) != "down" {
		t.Fatal("queued payload changed")
	}
	if stats.ExchangeRequests.Load() != 2 || stats.ExchangeReplies.Load() != 2 || stats.Received.Load() != 1 || stats.Sent.Load() != 1 {
		t.Fatal(stats.Snapshot())
	}
	backend.SetReadDeadline(time.Now().Add(60 * time.Millisecond))
	if _, _, err = backend.ReadFromUDP(buf); err == nil {
		t.Fatal("duplicate reached backend")
	}
	// A delayed authenticated request must receive its response at its own
	// source, even after a newer request establishes a different endpoint.
	older, _ := a.sealSized(wire.Exchange, nil, 1400, time.Now())
	olderSeq := a.send
	newer, _ := a.sealSized(wire.Exchange, nil, 1440, time.Now())
	newerSeq := a.send
	second, e := net.DialUDP("udp4", nil, public.LocalAddr().(*net.UDPAddr))
	if e != nil {
		t.Fatal(e)
	}
	defer second.Close()
	second.Write(newer)
	second.SetReadDeadline(time.Now().Add(time.Second))
	n, e = second.Read(buf)
	if e != nil || n != 1440 {
		t.Fatal("new endpoint response", e)
	}
	f, ok, _ = a.open(buf[:n], time.Now())
	if !ok || binary.BigEndian.Uint64(f.Payload[:8]) != newerSeq {
		t.Fatal("new request correlation")
	}
	conn.Write(older)
	conn.SetReadDeadline(time.Now().Add(time.Second))
	n, e = conn.Read(buf)
	if e != nil || n != 1400 {
		t.Fatal("old request response went to wrong endpoint", e)
	}
	f, ok, _ = a.open(buf[:n], time.Now())
	if !ok || binary.BigEndian.Uint64(f.Payload[:8]) != olderSeq {
		t.Fatal("old request correlation")
	}
}

func TestExchangeQueueBoundAndExpiry(t *testing.T) {
	s := new(Stats)
	q := exchangeQueue{stats: s}
	now := time.Now()
	for i := 0; i < 200; i++ {
		q.push([]byte{1}, now)
	}
	if len(q.items) != 128 || s.ExchangeQueueDropped.Load() != 72 {
		t.Fatal("unbounded queue")
	}
	if q.pop(now.Add(501*time.Millisecond)) != nil || len(q.items) != 0 || s.ExchangeQueueDropped.Load() != 200 {
		t.Fatal("expired data sent")
	}
	q.push([]byte{2}, now.Add(time.Second))
	q.clear()
	if len(q.items) != 0 || s.ExchangeQueueDropped.Load() != 201 {
		t.Fatal("reset retained data")
	}
}

func TestExchangeRecoversAfterServerStateLoss(t *testing.T) {
	public, backend, local, wg := socket(t), socket(t), socket(t), socket(t)
	key := keyString()
	sc := config.Config{Mode: "server", Transport: "exchange", Listen: public.LocalAddr().String(), WireGuard: backend.LocalAddr().String(), Key: key, Profile: "balanced"}
	cc := config.Config{Mode: "client", Transport: "exchange", Listen: local.LocalAddr().String(), Endpoint: sc.Listen, WireGuard: wg.LocalAddr().String(), Key: key, Profile: "balanced"}
	var serverStop context.CancelFunc
	var serverDone chan error
	start := func(c *net.UDPConn) {
		ctx, cancel := context.WithCancel(context.Background())
		serverStop = cancel
		serverDone = make(chan error, 1)
		ch := serverDone
		go func() { ch <- RunServer(ctx, c, sc, new(Stats)) }()
	}
	start(public)
	defer func() {
		serverStop()
		select {
		case e := <-serverDone:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(2 * time.Second):
			t.Error("server shutdown")
		}
	}()
	remote, e := net.DialUDP("udp4", nil, public.LocalAddr().(*net.UDPAddr))
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	stats := new(Stats)
	go func() { done <- RunClient(ctx, local, remote, cc, stats) }()
	defer func() {
		cancel()
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(2 * time.Second):
			t.Error("client shutdown")
		}
	}()
	wg.WriteToUDP([]byte("before"), local.LocalAddr().(*net.UDPAddr))
	buf := make([]byte, 2048)
	backend.SetReadDeadline(time.Now().Add(3 * time.Second))
	if n, _, e := backend.ReadFromUDP(buf); e != nil || string(buf[:n]) != "before" {
		t.Fatal("initial association", e)
	}
	serverStop()
	if e := <-serverDone; e != nil {
		t.Fatal(e)
	}
	restarted, e := net.ListenUDP("udp4", public.LocalAddr().(*net.UDPAddr))
	if e != nil {
		t.Fatal(e)
	}
	start(restarted)
	deadline := time.Now().Add(22 * time.Second)
	for time.Now().Before(deadline) {
		wg.WriteToUDP([]byte("after"), local.LocalAddr().(*net.UDPAddr))
		backend.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
		n, peer, e := backend.ReadFromUDP(buf)
		if e != nil {
			continue
		}
		if string(buf[:n]) != "after" {
			continue
		}
		backend.WriteToUDP([]byte("recovered"), peer)
		wg.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, _, e = wg.ReadFromUDP(buf)
		if e != nil || string(buf[:n]) != "recovered" {
			t.Fatal("return after reconnect", e)
		}
		if stats.Established.Load() < 2 {
			t.Fatal("old session was reused")
		}
		return
	}
	t.Fatal("did not establish a fresh session after server restart", stats.Snapshot())
}

func TestExchangeRejectMalformedSessionFrames(t *testing.T) {
	key := bytes.Repeat([]byte{2}, 32)
	id := wire.NewID()
	token := bytes.Repeat([]byte{3}, 32)
	c, _ := newAssociationMode(key, id, token, true, time.Now(), true)
	s, _ := newAssociationMode(key, id, token, false, time.Now(), true)
	for _, f := range []wire.Frame{
		{Kind: wire.Data, Payload: []byte{1}},
		{Kind: wire.ExchangeReply, Payload: make([]byte, 8)},
		{Kind: wire.ExchangeReply, Payload: append(make([]byte, 8), 2)},
		{Kind: wire.Exchange, Payload: make([]byte, wire.ExchangeMaxPayload+1)},
	} {
		f.ID = id
		f.Sequence++
		f.Timestamp = time.Now().Unix()
		p, e := c.codec.Seal(f, wire.MaxDatagram)
		if e != nil {
			t.Fatal(e)
		}
		if _, ok, _ := s.open(p, time.Now()); ok {
			t.Fatal("malformed session accepted")
		}
	}
}
