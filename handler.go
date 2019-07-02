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
	"errors"
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
}

// NewHandler creates a Handler which serves IP addresses from the specified
// subnets.
func NewHandler(subnets []*net.IPNet) (*Handler, error) {
	// At least one subnet must be specified to serve.
	if len(subnets) == 0 {
		return nil, errors.New("wgipam: NewHandler must have one or more subnets to serve")
	}

	// Split subnets by family and create IPStores for each.
	var sub4, sub6 []*net.IPNet
	for _, s := range subnets {
		if s.IP.To4() != nil {
			sub4 = append(sub4, s)
		} else {
			sub6 = append(sub6, s)
		}
	}

	var h Handler

	if len(sub4) > 0 {
		ips, err := NewIPStore(sub4)
		if err != nil {
			return nil, err
		}
		h.IPv4 = ips
	}

	if len(sub6) > 0 {
		ips, err := NewIPStore(sub6)
		if err != nil {
			return nil, err
		}
		h.IPv6 = ips
	}

	return &h, nil
}

// RequestIP implements the wg-dynamic request_ip command.
func (h *Handler) RequestIP(src net.Addr, _ *wgdynamic.RequestIP) (*wgdynamic.RequestIP, error) {
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
		// TODO(mdlayher): also free any temporarily allocated IP addresses.
		h.logf(src, "out of IP addresses")
		return nil, &wgdynamic.Error{
			Number:  1,
			Message: "out of IP addresses",
		}
	}

	h.logf(src, "allocated IP addresses: IPv4: %s, IPv6: %s", ip4, ip6)

	return &wgdynamic.RequestIP{
		IPv4:       ip4,
		IPv6:       ip6,
		LeaseStart: time.Now(),
		LeaseTime:  10 * time.Second,
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
