//go:build !windows

package transport

import (
	"errors"
	"net"
)

// Linux uses batched/OOB socket reads. Do not silently claim socket-boundary
// coverage there or disable its optimizations for a Windows diagnostic.
func traceH3Socket(_ *net.UDPConn, _ *PacketTrace) (net.PacketConn, error) {
	return nil, errors.New("H3 preflight socket tracing currently requires Windows")
}
