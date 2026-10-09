package transport

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"
)

// PacketTrace is an opt-in, bounded socket-boundary diagnostic. It records hashes
// of UDP datagrams, never their bytes or configuration secrets. In normal CWG
// use, both the outer and local WireGuard datagrams are already encrypted.
// Successful writes mean acceptance by the local socket, not network delivery.
type PacketTrace struct {
	mu        sync.Mutex
	file      *os.File
	remaining int
	err       error
}

type traceRecord struct {
	UTC    string `json:"utc"`
	Event  string `json:"event"`
	Local  string `json:"local,omitempty"`
	Remote string `json:"remote,omitempty"`
	Bytes  int    `json:"bytes,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

// OpenPacketTrace refuses to overwrite existing files and limits each run to
// 100,000 packet records (about 30 MB). Trace errors never interrupt forwarding.
func OpenPacketTrace(path string) (*PacketTrace, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	return &PacketTrace{file: f, remaining: 100000}, nil
}

func (t *PacketTrace) record(event string, conn *net.UDPConn, addr netip.AddrPort, data []byte) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.file == nil || t.err != nil || t.remaining < 0 {
		return
	}
	if t.remaining == 0 {
		t.err = json.NewEncoder(t.file).Encode(traceRecord{UTC: time.Now().UTC().Format(time.RFC3339Nano), Event: "limit_reached"})
		t.remaining = -1
		return
	}
	remote := addr.String()
	if !addr.IsValid() {
		if r := conn.RemoteAddr(); r != nil {
			remote = r.String()
		} else {
			remote = ""
		}
	}
	digest := sha256.Sum256(data)
	t.err = json.NewEncoder(t.file).Encode(traceRecord{
		UTC: time.Now().UTC().Format(time.RFC3339Nano), Event: event,
		Local: conn.LocalAddr().String(), Remote: remote, Bytes: len(data), SHA256: hex.EncodeToString(digest[:]),
	})
	t.remaining--
}

func (t *PacketTrace) Close() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.file == nil {
		return t.err
	}
	err := t.file.Close()
	t.file = nil
	if t.err != nil {
		return t.err
	}
	return err
}
