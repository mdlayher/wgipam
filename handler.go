// Copyright 2019 Matt Layher
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package wgipam

import (
	"fmt"
	"log"
	"net"
	"time"

	"github.com/mdlayher/wgdynamic-go"
)

const namespace = "wgipamd"

// A Handler handles IP allocation requests using the wg-dynamic protocol.
type Handler struct {
	// Leases specifies a Store for Lease storage. Leases must be non-nil or
	// the Handler will panic.
	Leases LeaseStore

	// IPs specifies an IPAllocator for allocating and freeing client addresses.
	// IPs must be non-nil or the Handler will panic.
	IPs IPAllocator

	// LeaseDuration specifies the amount of time leases may exist before they
	// expire and are purged.
	LeaseDuration time.Duration

	// Log specifies a logger for the Server. If nil, all logs are discarded.
	Log *log.Logger

	// Metrics specifies Prometheus metrics for the Handler. If nil, metrics
	// are not collected.
	Metrics *HandlerMetrics

	// TODO(mdlayher): perhaps generalize NewRequest like net/http.ConnState?

	// NewRequest specifies an optional hook which will be invoked when a new
	// request is received. The source address src of the remote client is
	// passed to NewRequest. If NewRequest is nil, it is a no-op.
	NewRequest func(src net.Addr)
}

// RequestIP implements the wg-dynamic request_ip command.
func (h *Handler) RequestIP(src net.Addr, req *wgdynamic.RequestIP) (*wgdynamic.RequestIP, error) {
	const op = "request_ip"

	if h.NewRequest != nil {
		// Hook is active, inform the caller of this request.
		h.NewRequest(src)
	}

	h.metrics(func() {
		h.Metrics.RequestsTotal.WithLabelValues(op).Inc()
	})

	res, err := h.requestIP(src, req)
	if err == nil {
		return res, nil
	}

	h.metrics(func() {
		// Note the type of error that occurs, but if the caller didn't
		// return a specific error type, assume it's a generic error.
		werr, ok := err.(*wgdynamic.Error)
		if !ok {
			h.Metrics.ErrorsTotal.WithLabelValues(op, "generic").Inc()
			return
		}

		var typ string
		switch werr {
		case wgdynamic.ErrInvalidRequest:
			typ = "invalid_request"
		case wgdynamic.ErrUnsupportedProtocol:
			typ = "unsupported_protocol"
		case wgdynamic.ErrIPUnavailable:
			typ = "ip_unavailable"
		default:
			typ = "unknown"
		}

		h.Metrics.ErrorsTotal.WithLabelValues(op, typ).Inc()
	})

	return nil, err
}

func (h *Handler) requestIP(src net.Addr, req *wgdynamic.RequestIP) (*wgdynamic.RequestIP, error) {
	// Check for an existing lease.
	key := strKey(src.String())
	l, ok, err := h.Leases.Lease(key)
	if err != nil {
		return nil, err
	}
	if !ok {
		// No lease, create a new lease.
		return h.newLease(src, req)
	}

	if time.Since(l.Start.Add(l.Length)) < 0 {
		// Lease has not expired, renew it.
		return h.renewLease(src, req, l)
	}

	// Clean up data related to the existing lease and create a new one.
	h.logf(src, "deleting expired lease and freeing IP addresses: %s", l)

	// TODO(mdlayher): better error handling, Prometheus metrics, etc.
	// Should failure to delete due to an item not existing actually be an
	// error? It seems like it'll create more noise than necessary.
	if err := h.Leases.DeleteLease(key); err != nil {
		h.logf(src, "failed to delete lease %s: %v", l, err)
	}

	for _, ip := range l.IPs {
		if err := h.IPs.Free(ip); err != nil {
			h.logf(src, "failed to free IP address %s: %v", ip, err)
		}
	}

	return h.newLease(src, req)
}

func (h *Handler) newLease(src net.Addr, req *wgdynamic.RequestIP) (*wgdynamic.RequestIP, error) {
	var family Family
	if len(req.IPs) == 0 {
		// No IPs specified, assume DualStack.
		family = DualStack
	} else {
		// Determine which IP families the client would like addresses for.
		for _, ip := range req.IPs {
			// For the new lease logic, refuse any non-zero IP requests to
			// force the client to retry and accept our allocation.
			if !ip.IP.Equal(net.IPv4zero) && !ip.IP.Equal(net.IPv6zero) {
				h.logf(src, "refusing to create new lease for address %s, informing client IP is unavailable", ip)
				return nil, wgdynamic.ErrIPUnavailable
			}

			// If multiple families are specified, bitwise OR will produce the
			// DualStack Family value.
			family |= ipFamily(ip)
		}
	}

	ips, ok, err := h.IPs.Allocate(family)
	if err != nil {
		return nil, err
	}
	if !ok {
		h.logf(src, "out of IP addresses")
		return nil, wgdynamic.ErrIPUnavailable
	}

	l := &Lease{
		IPs:    ips,
		Start:  timeNow(),
		Length: h.LeaseDuration,
	}

	h.logf(src, "creating new IP address lease: %s", l)

	if err := h.Leases.SaveLease(strKey(src.String()), l); err != nil {
		return nil, err
	}

	return &wgdynamic.RequestIP{
		IPs:        ips,
		LeaseStart: l.Start,
		LeaseTime:  l.Length,
	}, nil
}

// renewLease renews a Lease for clients who have an existing Lease.
func (h *Handler) renewLease(src net.Addr, req *wgdynamic.RequestIP, l *Lease) (*wgdynamic.RequestIP, error) {
	// If the client specified addresses in its request, ensure that they are
	// either zero or match the addresses we have from the previous lease.
	for _, ip := range req.IPs {
		if ip.IP.Equal(net.IPv4zero) || ip.IP.Equal(net.IPv6zero) {
			// No problem, client may have restarted and is going through the
			// new IP flow, but we already have a lease available for them.
			continue
		}

		// This address must be found within the leased IPs.
		var found bool
		for _, lip := range l.IPs {
			if ip.IP.Equal(lip.IP) {
				found = true
				break
			}
		}

		if !found {
			h.logf(src, "refusing to renew lease for address %s, informing client IP is unavailable", ip)
			return nil, wgdynamic.ErrIPUnavailable
		}
	}

	// We have a current lease, honor it and update its expiration time.
	l.Start = timeNow()
	l.Length = h.LeaseDuration

	h.logf(src, "renewing IP address lease: %s", l)

	if err := h.Leases.SaveLease(strKey(src.String()), l); err != nil {
		return nil, err
	}

	return &wgdynamic.RequestIP{
		IPs:        l.IPs,
		LeaseStart: l.Start,
		LeaseTime:  l.Length,
	}, nil
}

// logf logs a formatted message about src, if h.Log is configured.
func (h *Handler) logf(src net.Addr, format string, v ...interface{}) {
	if h.Log == nil {
		return
	}

	ta, ok := src.(*net.TCPAddr)
	if !ok {
		// Unknown address type, just print the whole thing.
		h.Log.Printf("%s: %s", src, fmt.Sprintf(format, v...))
		return
	}

	// Port and zone aren't necessary.
	h.Log.Printf("%s: %s", ta.IP, fmt.Sprintf(format, v...))
}

// metrics invokes fn if h.Metrics is configured.
func (h *Handler) metrics(fn func()) {
	if h.Metrics == nil {
		return
	}

	fn()
}

func panicf(format string, a ...interface{}) {
	panic(fmt.Sprintf(format, a...))
}
