package transport

import (
	"encoding/binary"
	"fmt"
	"net"
	"syscall"

	"golang.org/x/sys/windows"
)

// IP_UNICAST_IF is 31 in the Windows SDK's ws2ipdef.h. Its setter accepts
// network byte order; its getter returns host byte order.
const ipUnicastIF = 31

func endpointInterfaceControl(index int) (func(string, string, syscall.RawConn) error, error) {
	if index == 0 {
		return nil, nil
	}
	iface, err := net.InterfaceByIndex(index)
	if err != nil {
		return nil, fmt.Errorf("select outer interface %d: %w", index, err)
	}
	if iface.Flags&net.FlagUp == 0 {
		return nil, fmt.Errorf("outer interface %d is down", index)
	}
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], uint32(index))
	value := int(binary.NativeEndian.Uint32(encoded[:]))
	return func(network, address string, raw syscall.RawConn) error {
		if network != "udp4" {
			return fmt.Errorf("cannot bind outer interface for %s", network)
		}
		var socketErr error
		err := raw.Control(func(fd uintptr) {
			handle := windows.Handle(fd)
			if socketErr = windows.SetsockoptInt(handle, windows.IPPROTO_IP, ipUnicastIF, value); socketErr != nil {
				return
			}
			var selected int
			selected, socketErr = windows.GetsockoptInt(handle, windows.IPPROTO_IP, ipUnicastIF)
			if socketErr == nil && selected != index {
				socketErr = fmt.Errorf("requested interface %d but socket reports %d", index, selected)
			}
		})
		if err != nil {
			return err
		}
		if socketErr != nil {
			return fmt.Errorf("bind outer interface %d: %w", index, socketErr)
		}
		return nil
	}, nil
}
