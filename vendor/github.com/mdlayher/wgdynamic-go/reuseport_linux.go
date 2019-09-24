//+build linux

package wgdynamic

import (
	"syscall"

	"golang.org/x/sys/unix"
)

func reusePort(_, _ string, c syscall.RawConn) error {
	var err error
	c.Control(func(fd uintptr) {
		err = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
	})
	return err
}
