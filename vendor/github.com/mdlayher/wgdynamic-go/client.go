package wgdynamic

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// port is the well-known port for wg-dynamic.
const port = 970

// serverIP is the well-known server IPv6 address for wg-dynamic.
var serverIP = &net.IPNet{
	IP:   net.ParseIP("fe80::"),
	Mask: net.CIDRMask(64, 128),
}

// A Client can request IP address assignment using the wg-dynamic protocol.
// Most callers should construct a client using NewClient, which will bind to
// well-known addresses for wg-dynamic communications.
type Client struct {
	// Dial specifies an optional function used to dial an arbitrary net.Conn
	// connection to a predetermined wg-dynamic server. This is only necessary
	// when using a net.Conn transport other than *net.TCPConn, and most callers
	// should use NewClient to construct a Client instead.
	Dial func(ctx context.Context) (net.Conn, error)
}

// NewClient creates a new Client bound to the specified WireGuard interface.
// NewClient will return an error if the interface does not have an IPv6
// link-local address configured.
func NewClient(iface string) (*Client, error) {
	// TODO(mdlayher): verify this is actually a WireGuard device.
	ifi, err := net.InterfaceByName(iface)
	if err != nil {
		return nil, err
	}

	addrs, err := ifi.Addrs()
	if err != nil {
		return nil, err
	}

	return newClient(ifi.Name, addrs)
}

// newClient constructs a Client which communicates using well-known wg-dynamic
// addresses. It is used as an entry point in tests.
func newClient(iface string, addrs []net.Addr) (*Client, error) {
	// Find a suitable link-local IPv6 address for wg-dynamic communication.
	llip, ok := linkLocalIPv6(addrs)
	if !ok {
		return nil, fmt.Errorf("wgdynamic: no link-local IPv6 address for interface %q", iface)
	}

	// Client will listen on a well-known port and send requests to the
	// well-known server address.
	return &Client{
		// By default, use the stdlib net.Dialer type.
		Dial: func(ctx context.Context) (net.Conn, error) {
			d := &net.Dialer{
				// The server expects the client to be bound to a specific
				// local address.
				LocalAddr: &net.TCPAddr{
					IP:   llip.IP,
					Port: port,
					Zone: iface,
				},
				// On Linux, pass SO_REUSEPORT to prevent a nuisance error about
				// the port being in use when the client makes a few calls
				// in succession.
				Control: reusePort,
			}

			// wg-dynamic TCP connections always use IPv6.
			return d.DialContext(ctx, "tcp6", (&net.TCPAddr{
				IP:   serverIP.IP,
				Port: port,
				Zone: iface,
			}).String())
		},
	}, nil
}

// RequestIP requests IP address assignment from a server. Fields within req
// can be specified to request specific IP address assignment parameters. If req
// is nil, the server will automatically perform IP address assignment.
//
// The provided Context must be non-nil. If the context expires before the
// request is complete, an error is returned.
func (c *Client) RequestIP(ctx context.Context, req *RequestIP) (*RequestIP, error) {
	// Don't allow the client to set lease start.
	if req != nil && !req.LeaseStart.IsZero() {
		return nil, errors.New("wgdynamic: clients cannot specify a lease start time")
	}

	// Use a separate variable for the output so we don't overwrite the
	// caller's request.
	var rip *RequestIP
	err := c.execute(ctx, func(rw io.ReadWriter) error {
		if err := sendRequestIP(rw, fromClient, req); err != nil {
			return err
		}

		rrip, err := parseRequestIP(newKVParser(rw))
		if err != nil {
			return err
		}

		rip = rrip
		return nil
	})
	if err != nil {
		return nil, err
	}

	return rip, nil
}

// deadlineNow is a time in the past that indicates a connection should
// immediately time out.
var deadlineNow = time.Unix(1, 0)

// execute executes fn with a network connection backing rw.
func (c *Client) execute(ctx context.Context, fn func(rw io.ReadWriter) error) error {
	conn, err := c.Dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Enable immediate connection cancelation via context by using the context's
	// deadline and also setting a deadline in the past if/when the context is
	// canceled. This pattern courtesy of @acln from #networking on Gophers Slack.
	dl, _ := ctx.Deadline()
	if err := conn.SetDeadline(dl); err != nil {
		return err
	}

	errC := make(chan error)
	go func() { errC <- fn(conn) }()

	select {
	case <-ctx.Done():
		if ctx.Err() == context.Canceled {
			if err := conn.SetDeadline(deadlineNow); err != nil {
				return err
			}
		}

		<-errC
		return ctx.Err()
	case err := <-errC:
		return err
	}
}

// linkLocalIPv6 finds a link-local IPv6 address in addrs. It returns true when
// one is found.
func linkLocalIPv6(addrs []net.Addr) (*net.IPNet, bool) {
	var llip *net.IPNet
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}

		// Only look for link-local IPv6 addresses.
		if ipn.IP.To4() == nil && ipn.IP.IsLinkLocalUnicast() {
			llip = ipn
			break
		}
	}

	return llip, llip != nil
}
