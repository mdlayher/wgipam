package wgdynamic

import "fmt"

// wg-dynamic defines 0 as "success", but we handle success with nil error
// in Go.

// Possible Error values.
var (
	ErrInvalidRequest = &Error{
		Number:  1,
		Message: "Invalid request",
	}
	ErrUnsupportedProtocol = &Error{
		Number:  2,
		Message: "Unsupported protocol",
	}
	ErrIPUnavailable = &Error{
		Number:  3,
		Message: "Chosen IP(s) unavailable",
	}
)

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
