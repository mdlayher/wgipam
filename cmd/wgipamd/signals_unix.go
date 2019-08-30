//+build !windows

package main

import (
	"os"
	"syscall"
)

// signals returns a list of signals which can interrupt this program.
func signals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}
