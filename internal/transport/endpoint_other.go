//go:build !windows

package transport

import (
	"errors"
	"syscall"
)

func endpointInterfaceControl(index int) (func(string, string, syscall.RawConn) error, error) {
	if index != 0 {
		return nil, errors.New("-interface-index is only supported on Windows")
	}
	return nil, nil
}
