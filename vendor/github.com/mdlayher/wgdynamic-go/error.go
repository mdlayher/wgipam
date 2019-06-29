package wgdynamic

import "fmt"

var _ error = &Error{}

// An Error is a wg-dynamic protocol error.
type Error struct {
	Number  int
	Message string
}

// Error implements error.
func (e *Error) Error() string {
	return fmt.Sprintf("wgdynamic: error %d: %s", e.Number, e.Message)
}
