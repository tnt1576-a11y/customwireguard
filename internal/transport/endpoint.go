package transport

import (
	"context"
	"errors"
	"net"
	"net/netip"
)

// DialEndpoint optionally pins the outer socket to a Windows IPv4 interface.
// Selection happens before connect, so source selection uses that interface too.
// A requested binding that cannot be applied must never silently fall back.
func DialEndpoint(ctx context.Context, endpoint string, interfaceIndex int) (*net.UDPConn, error) {
	if interfaceIndex < 0 || interfaceIndex > 0xffffff {
		return nil, errors.New("interface index must be in 0..16777215")
	}
	addr, err := netip.ParseAddrPort(endpoint)
	if err != nil {
		return nil, err
	}
	if interfaceIndex != 0 && !addr.Addr().Is4() {
		return nil, errors.New("interface binding currently requires an IPv4 server endpoint")
	}
	control, err := endpointInterfaceControl(interfaceIndex)
	if err != nil {
		return nil, err
	}
	dialer := net.Dialer{Control: control}
	network := "udp6"
	if addr.Addr().Is4() {
		network = "udp4"
	}
	conn, err := dialer.DialContext(ctx, network, endpoint)
	if err != nil {
		return nil, err
	}
	return conn.(*net.UDPConn), nil
}
