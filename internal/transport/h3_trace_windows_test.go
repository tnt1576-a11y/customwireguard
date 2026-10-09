package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
)

func TestH3ProbeSocketTraceAndBinding(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	public, backend := socket(t), socket(t)
	cc, sc := h3Configs(t, "127.0.0.1:51821", public.LocalAddr().String(), "127.0.0.1:51822", backend.LocalAddr().String())
	startH3Fixture(t, ctx, public, sc)
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	index := 0
	for _, iface := range interfaces {
		if iface.Flags&(net.FlagLoopback|net.FlagUp) == net.FlagLoopback|net.FlagUp {
			index = iface.Index
			break
		}
	}
	if index == 0 {
		t.Fatal("no loopback interface")
	}
	for _, binding := range []int{0, index} {
		path := filepath.Join(t.TempDir(), "trace.jsonl")
		trace, err := OpenPacketTrace(path)
		if err != nil {
			t.Fatal(err)
		}
		result, err := ProbeH3(ctx, cc, 2, H3ProbeOptions{InterfaceIndex: binding, Trace: trace})
		trace.Close()
		if err != nil || result["authenticated_responses"] != 2 {
			t.Fatalf("traced probe: %v %v", result, err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(cc.Key)) || bytes.Contains(data, []byte(h3Authorization(cc))) {
			t.Fatal("trace contains secret")
		}
		counts := map[string]int{}
		for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
			var record traceRecord
			if err := json.Unmarshal(line, &record); err != nil {
				t.Fatal(err)
			}
			if len(record.SHA256) != 64 || record.Bytes == 0 || record.Remote != cc.Endpoint {
				t.Fatalf("invalid record: %+v", record)
			}
			counts[record.Event]++
		}
		if counts["h3-write"] == 0 || counts["h3-read"] == 0 {
			t.Fatal("missing socket boundaries")
		}
	}
}

func TestH3TracePreservesSocketCapabilities(t *testing.T) {
	conn, err := traceH3Socket(socket(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := conn.(quic.OOBCapablePacketConn); !ok {
		t.Fatal("trace removed socket capabilities")
	}
}

func TestH3TimeoutStillRecordsWrites(t *testing.T) {
	public, backend := socket(t), socket(t) // Public socket deliberately never responds.
	cc, _ := h3Configs(t, "127.0.0.1:51821", public.LocalAddr().String(), "127.0.0.1:51822", backend.LocalAddr().String())
	path := filepath.Join(t.TempDir(), "failed.jsonl")
	trace, err := OpenPacketTrace(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	_, err = ProbeH3(ctx, cc, 1, H3ProbeOptions{Trace: trace})
	trace.Close()
	if err == nil {
		t.Fatal("silent server succeeded")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("h3-write")) || bytes.Contains(data, []byte("h3-read")) {
		t.Fatal("incorrect failed-handshake trace")
	}
}
