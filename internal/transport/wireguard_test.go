package transport

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"customwireguard/internal/config"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/tuntest"
)

// Real WireGuard Noise handshakes and encrypted data over real loopback UDP.
// Channel TUNs replace OS interfaces only; no admin privileges/routes are needed.
func TestRealWireGuardIPv4IPv6AndMTU(t *testing.T) {
	t.Run("raw", func(t *testing.T) { testRealWireGuard(t, false) })
	t.Run("h3", func(t *testing.T) { testRealWireGuard(t, true) })
	t.Run("paced-raw", func(t *testing.T) { testRealWireGuard(t, false, 1000) })
}

func testRealWireGuard(t *testing.T, h3 bool, pace ...int) {
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
	if len(pace) != 0 {
		clientCfg.PaceKbps, serverCfg.PaceKbps = pace[0], pace[0]
	}
	if h3 {
		clientCfg, serverCfg = h3Configs(t, local.LocalAddr().String(), public.LocalAddr().String(), clientPort, serverPort)
	}
	if e = clientWG.IpcSet(fmt.Sprintf("public_key=%s\nendpoint=%s\n", hex.EncodeToString(serverKey.PublicKey().Bytes()), local.LocalAddr())); e != nil {
		t.Fatal(e)
	}
	remote, e := net.DialUDP("udp4", nil, public.LocalAddr().(*net.UDPAddr))
	if e != nil {
		t.Fatal(e)
	}
	defer remote.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 2)
	cs, ss := new(Stats), new(Stats)
	if h3 {
		go func() { done <- RunH3Client(ctx, local, clientCfg, cs, 0) }()
		go func() { done <- RunH3Server(ctx, public, serverCfg, ss) }()
	} else {
		go func() { done <- RunClient(ctx, local, remote, clientCfg, cs) }()
		go func() { done <- RunServer(ctx, public, serverCfg, ss) }()
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
				packet := ipPacket(netip.MustParseAddr(src), netip.MustParseAddr(dst), size)
				select {
				case from.Outbound <- packet:
				case <-time.After(5 * time.Second):
					t.Fatal("TUN input timeout")
				}
				select {
				case got := <-to.Inbound:
					if !bytes.Equal(got, packet) {
						t.Fatal("WireGuard packet changed")
					}
				case <-time.After(8 * time.Second):
					t.Fatalf("WireGuard timeout ipv6=%t size=%d reverse=%t stats=%v/%v", ipv6, size, reverse, cs.Snapshot(), ss.Snapshot())
				}
			}
		}
	}
	if cs.Established.Load() == 0 || ss.Established.Load() == 0 {
		t.Fatal("no session established")
	}
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
