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

// A Handler handles IP allocation requests using the wg-dynamic protocol.
type Handler struct {
	// Log specifies a logger for the Server. If nil, all logs are discarded.
	Log *log.Logger

	// IPv4 and IPv6 specify IPStores for IPv4 and IPv6 addresses, respectively.
	// If either are nil, addresses will not be allocated for that family.
	IPv4, IPv6 IPStore

	// Leases specifies a LeaseStore for Lease storage. If nil, Leases are not
	// used, and all incoming requests will allocate new IP addresses.
	Leases LeaseStore

	// TODO(mdlayher): perhaps generalize NewRequest like net/http.ConnState?

	// NewRequest specifies an optional hook which will be invoked when a new
	// request is received. The source address src of the remote client is
	// passed to NewRequest. If NewRequest is nil, it is a no-op.
	NewRequest func(src net.Addr)
}

// RequestIP implements the wg-dynamic request_ip command.
func (h *Handler) RequestIP(src net.Addr, req *wgdynamic.RequestIP) (*wgdynamic.RequestIP, error) {
	if h.NewRequest != nil {
		// Hook is active, inform the caller of this request.
		h.NewRequest(src)
	}

	if h.Leases == nil {
		// Lease store is not configured, always allocate new addresses.
		res, err := h.allocate(src, req)
		if err != nil {
			return nil, err
		}

		h.logf(src, "leases not configured, allocating ephemeral IP addresses: IPv4: %s, IPv6: %s", res.IPv4, res.IPv6)
		return res, nil
	}

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
		return h.renewLease(src, l)
	}

	// Clean up data related to the existing lease and create a new one.
	h.logf(src, "deleting expired lease and freeing IP addresses: %s", l)

	// TODO(mdlayher): better error handling, Prometheus metrics, etc.
	// Should failure to delete due to an item not existing actually be an
	// error? It seems like it'll create more noise than necessary.
	if err := h.Leases.Delete(key); err != nil {
		h.logf(src, "failed to delete lease %s: %v", l, err)
	}
	if err := free(h.IPv4, l.IPv4); err != nil {
		h.logf(src, "failed to free IPv4 address %s: %v", l.IPv4, err)
	}
	if err := free(h.IPv6, l.IPv6); err != nil {
		h.logf(src, "failed to free IPv6 address %s: %v", l.IPv6, err)
	}

	return h.newLease(src, req)
}

// allocate handles new IP address allocation.
func (h *Handler) allocate(src net.Addr, _ *wgdynamic.RequestIP) (*wgdynamic.RequestIP, error) {
	// TODO(mdlayher): honor requests for specific IP addresses.

	ip4, ok4, err := allocate(h.IPv4)
	if err != nil {
		return nil, err
	}
	ip6, ok6, err := allocate(h.IPv6)
	if err != nil {
		return nil, err
	}

	// If no pools are configured, or either pool is configured and runs out
	// of addresses, return a protocol error.
	if h.IPv4 == nil && h.IPv6 == nil ||
		(h.IPv4 != nil && !ok4) ||
		(h.IPv6 != nil && !ok6) {
		h.logf(src, "out of IP addresses")

		// We must also free the address of the other pool in case just one of
		// the address family pools was empty.
		if ip4 != nil {
			if err := free(h.IPv4, ip4); err != nil {
				// TODO(mdlayher): better error handling, Prometheus metrics, etc.
				h.logf(src, "failed to free temporarily allocated IPv4 address %s: %v", ip4, err)
			}
		}
		if ip6 != nil {
			if err := free(h.IPv6, ip6); err != nil {
				// TODO(mdlayher): better error handling, Prometheus metrics, etc.
				h.logf(src, "failed to free temporarily allocated IPv6 address %s: %v", ip6, err)
			}
		}

		return nil, &wgdynamic.Error{
			Number:  1,
			Message: "out of IP addresses",
		}
	}

	return &wgdynamic.RequestIP{
		IPv4:       ip4,
		IPv6:       ip6,
		LeaseStart: time.Now(),
		LeaseTime:  10 * time.Second,
	}, nil
}

// newLease creates a new Lease for a client.
func (h *Handler) newLease(src net.Addr, req *wgdynamic.RequestIP) (*wgdynamic.RequestIP, error) {
	res, err := h.allocate(src, req)
	if err != nil {
		return nil, err
	}

	l := &Lease{
		IPv4:   res.IPv4,
		IPv6:   res.IPv6,
		Start:  res.LeaseStart,
		Length: res.LeaseTime,
	}

	h.logf(src, "creating new IP address lease: %s", l)

	if err := h.Leases.Save(strKey(src.String()), l); err != nil {
		return nil, err
	}

	return res, nil
}

// renewLease renews a Lease for clients who have an existing Lease.
func (h *Handler) renewLease(src net.Addr, l *Lease) (*wgdynamic.RequestIP, error) {
	// We have a current lease, honor it and update its expiration time.
	// TODO(mdlayher): lease expiration, parameterize expiration time.
	l.Start = time.Now()
	l.Length = 10 * time.Second

	h.logf(src, "renewing IP address lease: %s", l)

	if err := h.Leases.Save(strKey(src.String()), l); err != nil {
		return nil, err
	}

	return &wgdynamic.RequestIP{
		IPv4:       l.IPv4,
		IPv6:       l.IPv6,
		LeaseStart: l.Start,
		LeaseTime:  l.Length,
	}, nil
}

// allocate allocates IP addresses from ips. If ips is nil, it returns early.
func allocate(ips IPStore) (*net.IPNet, bool, error) {
	if ips == nil {
		// Shortcut to make calling code more concise.
		return nil, false, nil
	}

	return ips.Allocate()
}

// free frees an IP addresses in ips. If ips is nil, it returns early.
func free(ips IPStore, ip *net.IPNet) error {
	if ips == nil {
		// Shortcut to make calling code more concise.
		return nil
	}

	return ips.Free(ip)
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
