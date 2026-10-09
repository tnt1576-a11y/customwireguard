package transport

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"sync"
	"time"

	"customwireguard/internal/config"
	masque "github.com/quic-go/masque-go"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/quic-go/quicvarint"
	"github.com/yosida95/uritemplate/v3"
)

// 1088-byte IPv4 tunnel MTU + WireGuard's 32 bytes, including its block padding.
// Fits HTTP Datagrams on a minimum-size 1200-byte QUIC path in either direction.
const H3MaxWireGuard = 1120
const h3Path = "/.well-known/masque/udp/{target_host}/{target_port}/"

func h3QUICConfig() *quic.Config {
	return &quic.Config{
		EnableDatagrams: true, InitialPacketSize: 1200, DisablePathMTUDiscovery: true,
		HandshakeIdleTimeout: 8 * time.Second, MaxIdleTimeout: 45 * time.Second,
		KeepAlivePeriod: 15 * time.Second, MaxIncomingStreams: 8,
		MaxIncomingUniStreams: 8, Allow0RTT: false,
	}
}

func h3Authorization(c config.Config) string {
	key, _ := c.Secret() // Config.Validate is required at the entry point.
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("cwg/h3/authorization/v1"))
	return "Bearer " + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func h3ClientTLS(c config.Config) (*tls.Config, error) {
	data, err := os.ReadFile(c.TLSCert)
	if err != nil {
		return nil, err
	}
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("tls_cert must contain exactly one pinned certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	endpoint := netip.MustParseAddrPort(c.Endpoint)
	return &tls.Config{
		MinVersion: tls.VersionTLS13, RootCAs: pool,
		ServerName: endpoint.Addr().String(), NextProtos: []string{http3.NextProtoH3},
		// Default chain, IP SAN and expiry verification remains enabled. This
		// additional check pins the exact certificate, even if it is a CA.
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 || !bytes.Equal(cs.PeerCertificates[0].Raw, cert.Raw) {
				return errors.New("server certificate does not match pin")
			}
			return nil
		},
	}, nil
}

type h3Session struct {
	udp    *net.UDPConn
	tr     *quic.Transport
	q      *quic.Conn
	http   *http3.ClientConn
	flow   *masque.Conn
	cancel context.CancelFunc
	stop   func() bool
}

func (s *h3Session) close() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.stop != nil {
		s.stop()
	}
	if s.q != nil {
		s.q.CloseWithError(0, "")
	}
	if s.flow != nil {
		s.flow.Close()
	}
	if s.tr != nil {
		s.tr.Close()
	}
	if s.udp != nil {
		s.udp.Close()
	}
}

func dialH3(ctx context.Context, c config.Config, index int, connect bool) (_ *h3Session, err error) {
	tlsConfig, err := h3ClientTLS(c)
	if err != nil {
		return nil, err
	}
	s := new(h3Session)
	defer func() {
		if err != nil {
			s.close()
		}
	}()
	sessionCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	// Bound both the QUIC handshake and HTTP request. Stop the timer after
	// acceptance, so a live UDP flow doesn't inherit a setup deadline.
	timer := time.AfterFunc(12*time.Second, cancel)
	defer timer.Stop()
	s.udp, err = ListenEndpoint(sessionCtx, c.Endpoint, index)
	if err != nil {
		return nil, err
	}
	s.udp.SetReadBuffer(2 << 20)
	s.udp.SetWriteBuffer(2 << 20)
	s.tr = &quic.Transport{Conn: s.udp}
	addr, _ := net.ResolveUDPAddr("udp", c.Endpoint)
	s.q, err = s.tr.Dial(sessionCtx, addr, tlsConfig, h3QUICConfig())
	if err != nil {
		return nil, err
	}
	s.stop = context.AfterFunc(sessionCtx, func() { s.q.CloseWithError(0, "") })
	ht := &http3.Transport{EnableDatagrams: true}
	s.http = ht.NewClientConn(s.q)
	if connect {
		template := uritemplate.MustNew("https://" + c.Endpoint + h3Path)
		req, e := masque.NewRequest(sessionCtx, template, c.H3Target)
		if e != nil {
			return nil, e
		}
		req.Header().Set("Authorization", h3Authorization(c))
		s.flow, _, err = masque.NewClientConn(s.http).Dial(req)
		if err != nil {
			return nil, err
		}
	}
	if err = sessionCtx.Err(); err != nil {
		return nil, err
	}
	return s, nil
}

// ProbeH3 verifies TLS pinning, HTTP/3 and authorization only. It deliberately
// creates no WireGuard association and makes no routing or interface changes.
func ProbeH3(ctx context.Context, c config.Config, count int) (map[string]any, error) {
	if count < 1 || count > 100 {
		return nil, errors.New("count must be 1..100")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	s, err := dialH3(ctx, c, 0, false)
	if err != nil {
		return nil, err
	}
	defer s.close()
	times := make([]float64, 0, count)
	for range count {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+c.Endpoint+"/health", nil)
		req.Header.Set("Authorization", h3Authorization(c))
		start := time.Now()
		resp, e := s.http.RoundTrip(req)
		if e != nil {
			return nil, e
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			return nil, fmt.Errorf("HTTP/3 health check returned %d", resp.StatusCode)
		}
		times = append(times, float64(time.Since(start).Microseconds())/1000)
	}
	return map[string]any{"transport": "h3", "authenticated_responses": len(times), "request_ms": times,
		"meaning": "Pinned TLS and authenticated HTTP/3 only; reliable HTTP requests do not measure UDP loss, WireGuard, routing, or DPI resistance."}, nil
}

func RunH3Client(ctx context.Context, local *net.UDPConn, c config.Config, stats *Stats, index int) error {
	if _, err := h3ClientTLS(c); err != nil {
		return err
	}
	channels, cleanup := readers(ctx, stats, local)
	defer cleanup()
	backoff := time.Second
	for ctx.Err() == nil {
		s, err := dialH3(ctx, c, index, true)
		if err == nil {
			backoff = time.Second
			stats.Established.Add(1)
			fmt.Fprintln(os.Stderr, "HTTP/3 CONNECT-UDP established; maximum WireGuard datagram 1120 bytes (IPv4 tunnel MTU 1088).")
			err = runH3Flow(ctx, s, local, channels[0], c, stats)
			s.close()
		}
		if ctx.Err() != nil {
			return nil
		}
		fmt.Fprintln(os.Stderr, "HTTP/3 connection interrupted; retrying:", err)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff = min(10*time.Second, backoff*2)
	}
	return nil
}

func runH3Flow(ctx context.Context, s *h3Session, local *net.UDPConn, input <-chan packet, c config.Config, stats *Stats) error {
	wgAddr := netip.MustParseAddrPort(c.WireGuard)
	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 65535)
		for {
			n, _, err := s.flow.ReadFrom(buf)
			if err != nil {
				done <- err
				return
			}
			if n > H3MaxWireGuard {
				stats.Oversize.Add(1)
				continue
			}
			if _, err = local.WriteToUDPAddrPort(buf[:n], wgAddr); err != nil {
				stats.SocketErrors.Add(1)
				done <- err
				return
			}
			stats.Received.Add(1)
			stats.Trace.record("write", local, wgAddr, buf[:n])
		}
	}()
	// Always join the receiver before returning or establishing another flow.
	receiverDone := false
	defer func() {
		s.q.CloseWithError(0, "")
		if !receiverDone {
			<-done
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-done:
			receiverDone = true
			return err
		case <-s.q.Context().Done():
			return context.Cause(s.q.Context())
		case p := <-input:
			if p.addr != wgAddr {
				stats.Rejected.Add(1)
				continue
			}
			if len(p.data) > H3MaxWireGuard {
				stats.Oversize.Add(1)
				continue
			}
			if _, err := s.flow.WriteTo(p.data, nil); err != nil {
				return err
			}
			stats.Sent.Add(1)
		}
	}
}

func h3Handler(c config.Config, stats *Stats) http.Handler {
	expected := h3Authorization(c)
	active := make(chan struct{}, 1)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			io.WriteString(w, "OK\n")
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte(expected)) != 1 {
			stats.Rejected.Add(1)
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/health" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		template, err := uritemplate.New("https://" + r.Host + h3Path)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		req, err := masque.ParseProxyRequest(r, template)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		// Compare before DNS or dialing: even an authenticated client cannot
		// turn this service into an open proxy or reach other local services.
		if req.Target != c.WireGuard {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		select {
		case active <- struct{}{}:
			defer func() { <-active }()
		default:
			w.WriteHeader(http.StatusConflict)
			return
		}
		conn := w.(interface {
			ReceivedSettings() <-chan struct{}
			Settings() *http3.Settings
		})
		select {
		case <-conn.ReceivedSettings():
		case <-r.Context().Done():
			return
		case <-time.After(8 * time.Second):
			w.WriteHeader(http.StatusRequestTimeout)
			return
		}
		if !conn.Settings().EnableDatagrams {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		addr, _ := net.ResolveUDPAddr("udp", c.WireGuard)
		backend, err := net.DialUDP("udp", nil, addr)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		backend.SetReadBuffer(2 << 20)
		backend.SetWriteBuffer(2 << 20)
		w.Header().Set(http3.CapsuleProtocolHeader, "?1")
		w.WriteHeader(http.StatusOK)
		str := w.(http3.HTTPStreamer).HTTPStream()
		stats.Established.Add(1)
		relayH3(r.Context(), backend, str, stats)
	})
}

func relayH3(ctx context.Context, backend *net.UDPConn, str *http3.Stream, stats *Stats) {
	ctx, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	stop := context.AfterFunc(ctx, func() {
		backend.Close()
		str.CancelRead(quic.StreamErrorCode(http3.ErrCodeNoError))
		str.CancelWrite(quic.StreamErrorCode(http3.ErrCodeNoError))
	})
	defer func() { cancel(); workers.Wait(); stop(); backend.Close(); str.Close() }()
	launch := func(f func()) { workers.Go(func() { defer cancel(); f() }) }
	launch(func() {
		for {
			data, err := str.ReceiveDatagram(ctx)
			if err != nil {
				return
			}
			id, n, err := quicvarint.Parse(data)
			if err != nil {
				stats.Rejected.Add(1)
				return
			}
			if id != 0 {
				continue
			}
			if len(data[n:]) > H3MaxWireGuard {
				stats.Oversize.Add(1)
				continue
			}
			if _, err = backend.Write(data[n:]); err != nil {
				stats.SocketErrors.Add(1)
				return
			}
			stats.Received.Add(1)
			stats.Trace.record("write", backend, netip.AddrPort{}, data[n:])
		}
	})
	launch(func() {
		buf := make([]byte, 65535)
		for {
			n, err := backend.Read(buf)
			if err != nil {
				return
			}
			if n > H3MaxWireGuard {
				stats.Oversize.Add(1)
				continue
			}
			stats.Trace.record("read", backend, netip.AddrPort{}, buf[:n])
			if err = str.SendDatagram(append([]byte{0}, buf[:n]...)); err != nil {
				stats.SocketErrors.Add(1)
				return
			}
			stats.Sent.Add(1)
		}
	})
	launch(func() {
		parser := http3.NewCapsuleParser(str)
		for {
			_, body, err := parser.Next()
			if err != nil {
				return
			}
			// No capsules are needed for context zero. RFC 9297 requires that
			// unknown types be ignored, with bounded memory use.
			if err = body.Discard(); err != nil {
				return
			}
		}
	})
	<-ctx.Done()
}

func RunH3Server(ctx context.Context, local *net.UDPConn, c config.Config, stats *Stats) error {
	cert, err := tls.LoadX509KeyPair(c.TLSCert, c.TLSKey)
	if err != nil {
		return err
	}
	srv := &http3.Server{
		TLSConfig:  &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}},
		QUICConfig: h3QUICConfig(), EnableDatagrams: true, MaxHeaderBytes: 8192,
		Handler: h3Handler(c, stats),
	}
	stop := context.AfterFunc(ctx, func() { srv.Close() })
	defer stop()
	defer srv.Close()
	err = srv.Serve(local)
	if ctx.Err() != nil || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
