package transport

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"customwireguard/internal/config"
)

func h3Configs(t *testing.T, local, public, clientWG, serverWG string) (config.Config, config.Config) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "tls")
	if err := config.InitializeTLS(dir, public); err != nil {
		t.Fatal(err)
	}
	cc := config.Config{Mode: "client", Listen: local, Endpoint: public, WireGuard: clientWG,
		Key: keyString(), Profile: "balanced", Transport: "h3", H3Target: serverWG, TLSCert: filepath.Join(dir, "server-cert.pem")}
	sc := config.Config{Mode: "server", Listen: public, WireGuard: serverWG, Key: cc.Key, Profile: "balanced",
		Transport: "h3", TLSCert: cc.TLSCert, TLSKey: filepath.Join(dir, "server-key.pem")}
	return cc, sc
}

func startH3Fixture(t *testing.T, ctx context.Context, public *net.UDPConn, sc config.Config) *Stats {
	t.Helper()
	stats := new(Stats)
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- RunH3Server(ctx, public, sc, stats) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("H3 server did not stop")
		}
	})
	return stats
}

func TestH3AuthorizationPinTargetAndDatagramLimits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	public, backend := socket(t), socket(t)
	cc, sc := h3Configs(t, "127.0.0.1:51821", public.LocalAddr().String(), "127.0.0.1:51822", backend.LocalAddr().String())
	stats := startH3Fixture(t, ctx, public, sc)
	if result, err := ProbeH3(ctx, cc, 2); err != nil || result["authenticated_responses"] != 2 {
		t.Fatalf("preflight: %v %v", result, err)
	}
	if stats.Established.Load() != 0 {
		t.Fatal("health check established a WireGuard flow")
	}
	bad := cc
	bad.Key = keyString()
	if _, err := ProbeH3(ctx, bad, 1); err == nil {
		t.Fatal("wrong credential accepted")
	}
	other, _ := h3Configs(t, cc.Listen, cc.Endpoint, cc.WireGuard, cc.H3Target)
	bad = cc
	bad.TLSCert = other.TLSCert
	if s, err := dialH3(ctx, bad, 0, true); err == nil {
		s.close()
		t.Fatal("wrong pin accepted")
	}
	bad = cc
	bad.H3Target = "127.0.0.1:22"
	if s, err := dialH3(ctx, bad, 0, true); err == nil {
		s.close()
		t.Fatal("unlisted destination accepted")
	}
	s, err := dialH3(ctx, cc, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if s.q.ConnectionState().TLS.NegotiatedProtocol != "h3" {
		t.Fatal("not HTTP/3")
	}
	if second, err := dialH3(ctx, cc, 0, true); err == nil {
		second.close()
		t.Fatal("concurrent flow accepted")
	}
	backend.SetDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 65535)
	for _, size := range []int{32, 148, H3MaxWireGuard} {
		payload := bytes.Repeat([]byte{byte(size)}, size)
		if _, err = s.flow.WriteTo(payload, nil); err != nil {
			t.Fatal(err)
		}
		n, peer, err := backend.ReadFromUDP(buf)
		if err != nil || !bytes.Equal(buf[:n], payload) {
			t.Fatalf("server %d: %v", size, err)
		}
		backend.WriteToUDP(buf[:n], peer)
		n, _, err = s.flow.ReadFrom(buf)
		if err != nil || !bytes.Equal(buf[:n], payload) {
			t.Fatalf("client %d: %v", size, err)
		}
	}
	// Too large for CWG's chosen MTU, but still fits the negotiated QUIC frame.
	if _, err = s.flow.WriteTo(make([]byte, H3MaxWireGuard+1), nil); err != nil {
		t.Fatal(err)
	}
	backend.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	if _, _, err = backend.ReadFromUDP(buf); err == nil {
		t.Fatal("oversize forwarded")
	}
	if stats.Oversize.Load() != 1 {
		t.Fatal("oversize not counted")
	}
	// An oversized response must also be dropped without killing the flow.
	backend.SetDeadline(time.Now().Add(3 * time.Second))
	s.flow.WriteTo([]byte("recover"), nil)
	n, peer, err := backend.ReadFromUDP(buf)
	if err != nil {
		t.Fatal(err)
	}
	backend.WriteToUDP(make([]byte, H3MaxWireGuard+1), peer)
	backend.WriteToUDP(buf[:n], peer)
	n, _, err = s.flow.ReadFrom(buf)
	if err != nil || string(buf[:n]) != "recover" {
		t.Fatalf("oversize recovery: %v", err)
	}
}

func TestH3OrdinaryRequestAndIPVerification(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	public, backend := socket(t), socket(t)
	cc, sc := h3Configs(t, "127.0.0.1:51821", public.LocalAddr().String(), "127.0.0.1:51822", backend.LocalAddr().String())
	startH3Fixture(t, ctx, public, sc)
	s, err := dialH3(ctx, cc, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+cc.Endpoint+"/", nil)
	resp, err := s.http.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.Status)
	}
	tlsConf, err := h3ClientTLS(cc)
	if err != nil {
		t.Fatal(err)
	}
	if tlsConf.InsecureSkipVerify {
		t.Fatal("verification disabled")
	}
	pair, err := tls.LoadX509KeyPair(sc.TLSCert, sc.TLSKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = cert.VerifyHostname("127.0.0.2"); err == nil {
		t.Fatal("wrong IP accepted")
	}
}
