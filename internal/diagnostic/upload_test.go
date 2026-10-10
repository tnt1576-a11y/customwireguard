package diagnostic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSmoothUploadIntegrityAndFailure(t *testing.T) {
	for _, bad := range []bool{false, true} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			data, err := io.ReadAll(r.Body)
			if err != nil || len(data) != 123457 || r.ContentLength != 123457 || r.Header.Get("X-CWG-Test-ID") != "fixture" {
				t.Error("invalid request", err, len(data))
			}
			for i, b := range data {
				if b != byte(i) {
					t.Error("bad pattern")
					break
				}
			}
			sum := sha256.Sum256(data)
			digest := hex.EncodeToString(sum[:])
			if bad {
				digest = "invalid"
			}
			json.NewEncoder(w).Encode(map[string]any{"received_bytes": len(data), "sha256": digest})
		}))
		result, err := Upload(context.Background(), strings.TrimPrefix(server.URL, "http://"), 123457, 3125000, "fixture")
		server.Close()
		if (err != nil) != bad || result["complete_and_verified"] != !bad {
			t.Fatal("integrity outcome", result, err)
		}
	}
}
func TestSmoothUploadRefusesExternalAndInvalid(t *testing.T) {
	for _, ep := range []string{"example.com:80", "192.0.2.1:80", "127.0.0.1:0", "127.0.0.1:80/path"} {
		if _, err := Upload(context.Background(), ep, 1, 1250000, "fixture"); err == nil {
			t.Fatal(ep)
		}
	}
	for _, n := range []int64{0, (64 << 20) + 1} {
		if _, err := Upload(context.Background(), "127.0.0.1:1", n, 1250000, "fixture"); err == nil {
			t.Fatal(n)
		}
	}
}
func TestSmoothPacerBoundsStallAndPreservesFraction(t *testing.T) {
	now := time.Now()
	interval := 2600 * time.Microsecond
	next := paceDeadline(now, now.Add(400*time.Microsecond), interval)
	if !next.Equal(now.Add(interval)) {
		t.Fatal("discarded fractional timer credit")
	}
	next = paceDeadline(now, now.Add(time.Second), interval)
	if next.Before(now.Add(time.Second)) || next.After(now.Add(time.Second+interval)) {
		t.Fatal("accumulated stall burst")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b := &pacedBody{ctx: ctx, count: 100, rate: 1250000}
	if _, err := b.Read(make([]byte, 10)); err == nil {
		t.Fatal("ignored cancellation")
	}
}
