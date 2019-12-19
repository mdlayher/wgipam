package wgdynamic

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
)

// errInternal is an unspecified internal server error.
var errInternal = &Error{
	Number:  1,
	Message: "Internal server error",
}

// A Server serves wg-dynamic protocol requests.
//
// Each exported function field implements a specific request. If any errors
// are returned, a protocol error is returned to the client. When the error is
// of type *Error, that protocol error is returned to the client. For generic
// errors, a generic protocol error is returned.
type Server struct {
	// RequestIP handles requests for IP address assignment. If nil, a generic
	// protocol error is returned to the client.
	RequestIP func(src net.Addr, r *RequestIP) (*RequestIP, error)

	// Log specifies an error logger for the Server. If nil, all error logs
	// are discarded.
	Log *log.Logger

	// Guards internal fields set when Serve is first called.
	mu sync.Mutex
	l  net.Listener
	wg *sync.WaitGroup
}

// Listen creates a net.Listener suitable for use with a Server and bound to
// the specified WireGuard interface. Listen will return an error if the
// does not have the well-known IPv6 link-local server address (fe80::/64)
// configured.
func Listen(iface string) (net.Listener, error) {
	ifi, err := net.InterfaceByName(iface)
	if err != nil {
		return nil, err
	}

	addrs, err := ifi.Addrs()
	if err != nil {
		return nil, err
	}

	llip, ok := linkLocalIPv6(addrs)
	if !ok || !llip.IP.Equal(serverIP.IP) || !bytes.Equal(llip.Mask, serverIP.Mask) {
		return nil, fmt.Errorf("wgdynamic: IPv6 server address %s must be assigned to interface %q", serverIP, iface)
	}

	return net.ListenTCP("tcp6", &net.TCPAddr{
		IP:   serverIP.IP,
		Port: port,
		Zone: iface,
	})
}

// Serve serves incoming requests by accepting connections from l.
func (s *Server) Serve(l net.Listener) error {
	// Initialize any necessary fields before starting the listener loop.
	s.mu.Lock()
	s.l = l
	s.wg = &sync.WaitGroup{}
	s.mu.Unlock()

	for {
		c, err := l.Accept()
		if err != nil {
			return err
		}

		// Guard s.wg to prevent a data race when another goroutine tries to
		// wait during a call to Close.
		s.mu.Lock()
		s.wg.Add(1)
		s.mu.Unlock()

		go func() {
			defer func() {
				// The C implementation immediately closes the connection once
				// a request is processed.
				_ = c.Close()
				s.wg.Done()
			}()

			s.handle(c)
		}()
	}
}

// Close closes the server listener and waits for all requests to complete.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	defer s.wg.Wait()
	return s.l.Close()
}

// handle handles an individual request. handle should be called in a goroutine.
func (s *Server) handle(c net.Conn) {
	p, cmd, err := parseRequest(c)
	if err != nil {
		s.logf("%s: error parsing request: %v", c.RemoteAddr().String(), err)
		return
	}

	// Pass the request to the appropriate handler.
	switch cmd {
	case "request_ip":
		err = s.handleRequestIP(c, p)
	default:
		// No such command.
		// TODO(mdlayher): use appropriate standard error number/message.
		err = errInternal
	}
	if err == nil {
		// No error handling needed.
		return
	}

	// If the function returned *Error, use that. Otherwise, log the error and
	// specify a generic error.
	werr, ok := err.(*Error)
	if !ok {
		s.logf("%s: %q error: %v", c.RemoteAddr().String(), cmd, err)
		werr = errInternal
	}

	// TODO(mdlayher): add serialization logic for Error type.
	_, _ = io.WriteString(c, fmt.Sprintf("%s=1\nerrno=%d\nerrmsg=%s\n\n",
		cmd, werr.Number, werr.Message))
}

// handleRequestIP processes a request_ip command.
func (s *Server) handleRequestIP(c net.Conn, p *kvParser) error {
	if s.RequestIP == nil {
		// Not implemented by caller.
		return errInternal
	}

	req, err := parseRequestIP(p)
	if err != nil {
		return err
	}

	res, err := s.RequestIP(c.RemoteAddr(), req)
	if err != nil {
		return err
	}

	return sendRequestIP(c, fromServer, res)
}

// logf creates a formatted log entry if s.Log is not nil.
func (s *Server) logf(format string, v ...interface{}) {
	if s.Log == nil {
		return
	}

	s.Log.Printf(format, v...)
}
