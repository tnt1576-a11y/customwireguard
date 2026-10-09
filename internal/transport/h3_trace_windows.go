package transport

import (
	"errors"
	"net"
	"net/netip"
)

func h3TracePeer(peer *net.UDPAddr) netip.AddrPort {
	ap := peer.AddrPort()
	return netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())
}

// quic-go uses ReadFrom/WriteTo on Windows. Embedding UDPConn preserves its
// socket interfaces, buffer configuration and DF behavior; only successful
// socket operations are logged. A successful write is NOT delivery evidence.
type h3TraceSocket struct {
	*net.UDPConn
	trace *PacketTrace
}

func traceH3Socket(conn *net.UDPConn, trace *PacketTrace) (net.PacketConn, error) {
	return &h3TraceSocket{UDPConn: conn, trace: trace}, nil
}

func (c *h3TraceSocket) ReadFrom(b []byte) (int, net.Addr, error) {
	n, addr, err := c.UDPConn.ReadFrom(b)
	if err == nil {
		if peer, ok := addr.(*net.UDPAddr); ok {
			c.trace.record("h3-read", c.UDPConn, h3TracePeer(peer), b[:n])
		}
	}
	return n, addr, err
}

func (c *h3TraceSocket) WriteTo(b []byte, addr net.Addr) (int, error) {
	n, err := c.UDPConn.WriteTo(b, addr)
	if peer, ok := addr.(*net.UDPAddr); ok {
		if err == nil {
			c.trace.record("h3-write", c.UDPConn, h3TracePeer(peer), b[:n])
		} else if !errors.Is(err, net.ErrClosed) {
			c.trace.record("h3-write-error", c.UDPConn, h3TracePeer(peer), nil)
		}
	}
	return n, err
}
