//+build !linux

package wgdynamic

import "syscall"

func reusePort(_, _ string, _ syscall.RawConn) error {
	return nil
}
