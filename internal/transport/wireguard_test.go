package transport

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"customwireguard/internal/config"
	"customwireguard/internal/wire"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/tuntest"
)

// Real WireGuard Noise handshakes and encrypted data over real loopback UDP.
// Channel TUNs replace OS interfaces only; no admin privileges/routes are needed.
func TestRealWireGuardIPv4IPv6AndMTU(t *testing.T) {
	t.Run("raw", func(t *testing.T) { testRealWireGuard(t, false, 0, false) })
	t.Run("h3", func(t *testing.T) { testRealWireGuard(t, true, 0, false) })
	t.Run("paced-raw", func(t *testing.T) { testRealWireGuard(t, false, 1000, false) })
	t.Run("exchange", func(t *testing.T) { testRealWireGuard(t, false, 0, true) })
	t.Run("adaptive-exchange", func(t *testing.T) { testRealWireGuard(t, false, -1, true) })
	t.Run("burst-exchange", func(t *testing.T) { testRealWireGuard(t, false, -2, true) })
	t.Run("reordered-exchange", func(t *testing.T) { testRealWireGuard(t, false, -3, true) })
	t.Run("repair-exchange", func(t *testing.T) { testRealWireGuard(t, false, -4, true) })
	t.Run("repair-with-loss", func(t *testing.T) { testRealWireGuard(t, false, -5, true) })
	t.Run("fec-exchange", func(t *testing.T) { testRealWireGuard(t, false, -6, true) })
	t.Run("fec-with-loss", func(t *testing.T) { testRealWireGuard(t, false, -7, true) })
	t.Run("fec-server-legacy-client", func(t *testing.T) { testRealWireGuard(t, false, -8, true) })
	t.Run("fec-burst-loss", func(t *testing.T) { testRealWireGuard(t, false, -9, true) })
	t.Run("shared-exchange", func(t *testing.T) { testRealWireGuard(t, false, -10, true) })
}

func testRealWireGuard(t *testing.T, h3 bool, pace int, exchange bool) {
	clientTun, serverTun := tuntest.NewChannelTUN(), tuntest.NewChannelTUN()
	clientWG := device.NewDevice(clientTun.TUN(), conn.NewStdNetBind(), device.NewLogger(device.LogLevelSilent, ""))
	serverWG := device.NewDevice(serverTun.TUN(), conn.NewStdNetBind(), device.NewLogger(device.LogLevelSilent, ""))
	defer clientWG.Close()
	defer serverWG.Close()
	clientKey, e := ecdh.X25519().GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	serverKey, e := ecdh.X25519().GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	psk := make([]byte, 32)
	rand.Read(psk)
	setup := func(d *device.Device, private, public []byte, allowed string) string {
		t.Helper()
		cfg := fmt.Sprintf("private_key=%s\nlisten_port=0\npublic_key=%s\npreshared_key=%s\n%s", hex.EncodeToString(private), hex.EncodeToString(public), hex.EncodeToString(psk), allowed)
		if e := d.IpcSet(cfg); e != nil {
			t.Fatal(e)
		}
		if e := d.Up(); e != nil {
			t.Fatal(e)
		}
		status, e := d.IpcGet()
		if e != nil {
			t.Fatal(e)
		}
		for line := range strings.SplitSeq(status, "\n") {
			if port, ok := strings.CutPrefix(line, "listen_port="); ok {
				return "127.0.0.1:" + port
			}
		}
		t.Fatal("WireGuard did not allocate UDP port")
		return ""
	}
	clientPort := setup(clientWG, clientKey.Bytes(), serverKey.PublicKey().Bytes(), "allowed_ip=10.77.0.1/32\nallowed_ip=fd77:77:77::1/128\n")
	serverPort := setup(serverWG, serverKey.Bytes(), clientKey.PublicKey().Bytes(), "allowed_ip=10.77.0.2/32\nallowed_ip=fd77:77:77::2/128\n")
	local, public := socket(t), socket(t)
	secret := keyString()
	clientCfg := config.Config{Mode: "client", Listen: local.LocalAddr().String(), Endpoint: public.LocalAddr().String(), WireGuard: clientPort, Key: secret, Profile: "balanced"}
	serverCfg := config.Config{Mode: "server", Listen: public.LocalAddr().String(), WireGuard: serverPort, Key: secret, Profile: "balanced"}
	if pace > 0 {
		clientCfg.PaceKbps, serverCfg.PaceKbps = pace, pace
	}
	if exchange {
		clientCfg.Transport, serverCfg.Transport = "exchange", "exchange"
		if pace < 0 {
			clientCfg.ExchangeMaxPPS = 6000
			clientCfg.ExchangeQueueMS = 60
			serverCfg.ExchangeQueueMS = 60
			if pace == -2 {
				clientCfg.ExchangeBurstMS, serverCfg.ExchangeBurstMS = 200, 200
			}
			if pace == -3 {
				clientCfg.ExchangeReorderMS, serverCfg.ExchangeReorderMS = 2, 2
			}
			if pace == -4 || pace == -5 {
				clientCfg.ExchangeReorderMS, serverCfg.ExchangeReorderMS = 2, 2
				clientCfg.ExchangeRepair, serverCfg.ExchangeRepair = true, true
			}
			if pace == -6 || pace == -7 || pace == -9 {
				clientCfg.ExchangeFEC, serverCfg.ExchangeFEC = true, true
				clientCfg.ExchangeReorderMS, serverCfg.ExchangeReorderMS = 2, 2
			}
			if pace == -8 {
				serverCfg.ExchangeFEC = true
			}
			if pace == -9 {
				// This cold-start fixture deliberately enqueues an entire burst
				// before the first handshake. Isolate recovery from the separate
				// 60 ms live small-packet deadline and adaptive ramp-up behavior.
				clientCfg.ExchangePPS = 200
				clientCfg.ExchangeQueueMS, serverCfg.ExchangeQueueMS = 500, 500
			}
			if pace == -10 {
				clientCfg.ExchangeShared, serverCfg.ExchangeShared = true, true
				clientCfg.ExchangeReorderMS, serverCfg.ExchangeReorderMS = 2, 2
				clientCfg.ExchangeRepair, serverCfg.ExchangeRepair = true, true
			}
		}
	}
	if h3 {
		clientCfg, serverCfg = h3Configs(t, local.LocalAddr().String(), public.LocalAddr().String(), clientPort, serverPort)
	}
	if e = clientWG.IpcSet(fmt.Sprintf("public_key=%s\nendpoint=%s\n", hex.EncodeToString(serverKey.PublicKey().Bytes()), local.LocalAddr())); e != nil {
		t.Fatal(e)
	}
	endpoint := public.LocalAddr().(*net.UDPAddr)
	var losses *[2]atomic.Uint32
	if pace == -5 || pace == -7 || pace == -9 {
		key, _ := clientCfg.Secret()
		dropCount := 1
		if pace == -9 {
			dropCount = 4
		}
		endpoint, losses = exchangeNativeLossRelay(t, endpoint, key, dropCount)
		clientCfg.Endpoint = endpoint.String()
	}
	remote, e := net.DialUDP("udp4", nil, endpoint)
	if e != nil {
		t.Fatal(e)
	}
	defer remote.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 2)
	cs, ss := new(Stats), new(Stats)
	var multiStats *MultiStats
	if h3 {
		go func() { done <- RunH3Client(ctx, local, clientCfg, cs, 0) }()
		go func() { done <- RunH3Server(ctx, public, serverCfg, ss) }()
	} else {
		go func() { done <- RunClient(ctx, local, remote, clientCfg, cs) }()
		if pace == -10 {
			multiStats = new(MultiStats)
			multi := config.MultiServer{Mode: "server-multi", Listen: serverCfg.Listen, MaxPPS: 8000, Users: []config.ServerUser{{Name: "native", Config: serverCfg}}}
			go func() { done <- RunMultiServer(ctx, public, multi, nil, multiStats) }()
		} else {
			go func() { done <- RunServer(ctx, public, serverCfg, ss) }()
		}
	}
	defer func() {
		cancel()
		for i := 0; i < 2; i++ {
			select {
			case e := <-done:
				if e != nil {
					t.Error(e)
				}
			case <-time.After(3 * time.Second):
				t.Error("transport shutdown timeout")
			}
		}
	}()
	families, sizes := []bool{false, true}, []int{64, 1280}
	if h3 {
		families, sizes = []bool{false}, []int{64, 1088}
	}
	for _, ipv6 := range families {
		for _, size := range sizes {
			for _, reverse := range []bool{false, true} {
				src, dst := "10.77.0.2", "10.77.0.1"
				if ipv6 {
					src, dst = "fd77:77:77::2", "fd77:77:77::1"
				}
				from, to := clientTun, serverTun
				if reverse {
					src, dst = dst, src
					from, to = to, from
				}
				count := 1
				if pace == -9 {
					count = 16
				}
				expected := make(map[string]bool, count)
				for i := 0; i < count; i++ {
					packet := ipPacket(netip.MustParseAddr(src), netip.MustParseAddr(dst), size)
					expected[string(packet)] = true
					select {
					case from.Outbound <- packet:
					case <-time.After(5 * time.Second):
						t.Fatal("TUN input timeout")
					}
				}
				for len(expected) > 0 {
					select {
					case got := <-to.Inbound:
						if !expected[string(got)] {
							t.Fatal("WireGuard packet changed or duplicated")
						}
						delete(expected, string(got))
					case <-time.After(8 * time.Second):
						t.Fatalf("WireGuard timeout ipv6=%t size=%d reverse=%t missing=%d stats=%v/%v", ipv6, size, reverse, len(expected), cs.Snapshot(), ss.Snapshot())
					}
				}
				if pace <= -4 {
					select {
					case <-to.Inbound:
						t.Fatal("backup copy escaped native WireGuard replay protection")
					case <-time.After(40 * time.Millisecond):
					}
				}
			}
		}
	}
	if multiStats != nil {
		multiStats.mu.RLock()
		ss = multiStats.users["native"]
		multiStats.mu.RUnlock()
		if ss == nil || cs.ExchangeGrantPPS.Load() == 0 {
			t.Fatal("shared rate negotiation not exercised")
		}
	}
	if cs.Established.Load() == 0 || ss.Established.Load() == 0 {
		t.Fatal("no session established")
	}
	if (pace == -4 || pace == -5) && (cs.ExchangeRepairSent.Load() == 0 || ss.ExchangeRepairSent.Load() == 0) {
		t.Fatal("backup copies were not exercised in both directions")
	}
	wantLoss := uint32(1)
	if pace == -9 {
		wantLoss = 4
	}
	if losses != nil && (losses[0].Load() != wantLoss || losses[1].Load() != wantLoss) {
		t.Fatal("loss injection did not drop native data in both directions")
	}
	if pace == -6 || pace == -7 || pace == -9 {
		if cs.ExchangeFECParity.Load() == 0 || ss.ExchangeFECParity.Load() == 0 {
			t.Fatal("FEC parity not exercised in both directions")
		}
		if (pace == -7 || pace == -9) && (cs.ExchangeFECRecovered.Load() < uint64(wantLoss) || ss.ExchangeFECRecovered.Load() < uint64(wantLoss)) {
			t.Fatal("real WireGuard loss was not recovered by FEC")
		}
	}
}

// Test-only relay knows this test's random outer key, so it can drop exactly
// a bounded number of nonempty native data packets in each direction, without dropping a
// handshake or keepalive instead. Inner WireGuard keys remain with the devices.
func exchangeNativeLossRelay(t *testing.T, server *net.UDPAddr, key []byte, dropCount int) (*net.UDPAddr, *[2]atomic.Uint32) {
	t.Helper()
	relay := socket(t)
	losses := new([2]atomic.Uint32)
	done := make(chan struct{})
	go func() {
		defer close(done)
		bootstrap, _ := wire.NewExchangeCodec(key, nil, true)
		var up, down *wire.Codec
		var client *net.UDPAddr
		buf := make([]byte, 65535)
		for {
			n, source, err := relay.ReadFromUDP(buf)
			if err != nil {
				return
			}
			data := buf[:n]
			destination := server
			var payload []byte
			index := 0
			if source.String() == server.String() {
				index = 1
				destination = client
				if welcome, err := bootstrap.Open(data, time.Now()); err == nil && welcome.Kind == wire.Welcome && len(welcome.Payload) == 32 {
					material := append(append([]byte{}, welcome.ID[:]...), welcome.Payload...)
					up, _ = wire.NewExchangeCodec(key, material, false)
					down, _ = wire.NewExchangeCodec(key, material, true)
				} else if down != nil {
					if f, err := down.Open(data, time.Now()); err == nil && f.Kind == wire.ExchangeReply && len(f.Payload) >= 9 {
						payload = f.Payload[9:]
					}
				}
			} else {
				client = source
				if up != nil {
					if f, err := up.Open(data, time.Now()); err == nil && f.Kind == wire.Exchange {
						payload = f.Payload
					}
				}
			}
			if len(payload) >= fecHeader && binary.BigEndian.Uint32(payload) == fecMagic {
				if payload[12] == 0 {
					payload = payload[fecHeader:]
				} else {
					payload = nil
				}
			}
			if len(payload) > 32 && binary.LittleEndian.Uint32(payload[:4]) == 4 && losses[index].Load() < uint32(dropCount) {
				losses[index].Add(1)
				continue
			}
			if destination != nil {
				if _, err := relay.WriteToUDP(data, destination); err != nil {
					return
				}
			}
		}
	}()
	t.Cleanup(func() {
		relay.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("loss relay did not stop")
		}
	})
	return relay.LocalAddr().(*net.UDPAddr), losses
}

func ipPacket(src, dst netip.Addr, size int) []byte {
	p := make([]byte, size)
	rand.Read(p)
	if src.Is4() {
		clear(p[:20])
		p[0] = 0x45
		p[8] = 64
		p[9] = 253
		binary.BigEndian.PutUint16(p[2:4], uint16(size))
		copy(p[12:16], src.AsSlice())
		copy(p[16:20], dst.AsSlice())
		var sum uint32
		for i := 0; i < 20; i += 2 {
			sum += uint32(binary.BigEndian.Uint16(p[i : i+2]))
		}
		for sum>>16 != 0 {
			sum = (sum & 65535) + (sum >> 16)
		}
		binary.BigEndian.PutUint16(p[10:12], ^uint16(sum))
	} else {
		clear(p[:40])
		p[0] = 0x60
		p[6] = 253
		p[7] = 64
		binary.BigEndian.PutUint16(p[4:6], uint16(size-40))
		copy(p[8:24], src.AsSlice())
		copy(p[24:40], dst.AsSlice())
	}
	return p
}
