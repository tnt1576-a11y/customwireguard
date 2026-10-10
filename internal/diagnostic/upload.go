// Package diagnostic supplies user-invoked synthetic transfers inside the VPN.
package diagnostic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"time"
)

var identifier = regexp.MustCompile("^[A-Za-z0-9-]{1,64}$")

// pacedBody preserves fractional timer credit, but bounds post-stall catch-up
// to one additional small block rather than a large accumulated allowance.
type pacedBody struct {
	ctx               context.Context
	count, sent, rate int64
	next              time.Time
}

func (b *pacedBody) Read(p []byte) (int, error) {
	if b.sent == b.count {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	if delay := time.Until(b.next); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-b.ctx.Done():
			return 0, b.ctx.Err()
		case <-timer.C:
		}
	}
	n := min(len(p), 8192, int(b.count-b.sent))
	for i := 0; i < n; i++ {
		p[i] = byte(b.sent + int64(i))
	}
	b.sent += int64(n)
	b.next = paceDeadline(b.next, time.Now(), time.Duration(int64(time.Second)*int64(n)/b.rate))
	return n, nil
}
func (b *pacedBody) Close() error { return nil }

func paceDeadline(previous, now time.Time, interval time.Duration) time.Time {
	floor := now.Add(-interval)
	if previous.Before(floor) {
		previous = floor
	}
	return previous.Add(interval)
}

func expectedHash(count int64) string {
	h := sha256.New()
	buf := make([]byte, 65536)
	for i := range buf {
		buf[i] = byte(i)
	}
	for count > 0 {
		n := min(count, int64(len(buf)))
		h.Write(buf[:n])
		count -= n
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Upload is bounded and accepts only the tunnel address or localhost, never a
// hostname, redirect, system proxy or public destination. No data files are read.
func Upload(ctx context.Context, endpoint string, count, rate int64, id string) (map[string]any, error) {
	addr, err := netip.ParseAddrPort(endpoint)
	if err != nil || addr.Port() == 0 || !(addr.Addr() == netip.MustParseAddr("127.0.0.1") || addr.Addr() == netip.MustParseAddr("10.77.0.1")) {
		return nil, errors.New("diagnostic endpoint must be literal 127.0.0.1 or 10.77.0.1 with a port")
	}
	if count < 1 || count > 64<<20 || !(rate == 1250000 || rate == 3125000 || rate == 6250000) || !identifier.MatchString(id) {
		return nil, errors.New("invalid diagnostic size, rate or identifier")
	}
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	body := &pacedBody{ctx: ctx, count: count, rate: rate}
	req, err := http.NewRequestWithContext(ctx, "POST", "http://"+endpoint+"/upload", body)
	if err != nil {
		return nil, err
	}
	req.ContentLength = count
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-CWG-Test-ID", id)
	// Keep OS TCP buffer autotuning; a tiny fixed send buffer would itself cap
	// throughput on a high-RTT tunnel.
	tr := &http.Transport{Proxy: nil, DisableKeepAlives: true, DisableCompression: true,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 6 * time.Second}).DialContext(ctx, "tcp4", endpoint)
		}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("diagnostic redirect refused") }}
	started := time.Now()
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var response struct {
		Received int64  `json:"received_bytes"`
		SHA256   string `json:"sha256"`
	}
	err = json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&response)
	elapsed := time.Since(started).Seconds()
	verified := err == nil && res.StatusCode == 200 && response.Received == count && response.SHA256 == expectedHash(count)
	result := map[string]any{"speed_upload": float64(response.Received) / elapsed, "time_total": elapsed, "received_bytes": response.Received, "complete_and_verified": verified, "response_code": res.StatusCode, "id": id}
	if !verified {
		return result, fmt.Errorf("diagnostic response/integrity verification failed (status %d)", res.StatusCode)
	}
	return result, nil
}
