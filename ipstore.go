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
	"net"
	"sync"

	"github.com/mikioh/ipaddr"
)

var (
	_ IPStore = &memoryIPStore{}
)

// An IPStore can allocate IP addresses. IPStore implementations should be
// configured to only return IPv4 or IPv6 addresses: never both from the same
// instance.
type IPStore interface {
	// Allocate allocates the next available IP address. It returns false
	// if no more IP addresses are available.
	Allocate() (ip *net.IPNet, ok bool, err error)

	// Free returns an allocated IP address to the IPStore. Free operations
	// should be idempotent; that is, an error should only be returned if the
	// free operation fails. Attempting to free an IP that did not already exist
	// should not return an error.
	Free(ip *net.IPNet) error
}

// A memoryIPStore is an in-memory IPStore implementation.
type memoryIPStore struct {
	mu  sync.Mutex
	m   map[*net.IPNet]struct{}
	c   *ipaddr.Cursor
	out bool
}

// DualStackIPStore returns an IPStore for each IPv4 and IPv6 address allocation.
// It is a convenience wrapper around NewIPStore that automatically allocates
// the input subnets into the appropriate IPStore.
func DualStackIPStore(subnets []*net.IPNet) (ip4s, ip6s IPStore, err error) {
	// At least one subnet must be specified to serve.
	if len(subnets) == 0 {
		return nil, nil, errors.New("wgipam: DualStackIPStore must have one or more subnets to serve")
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

	if len(sub4) > 0 {
		ips, err := MemoryIPStore(sub4)
		if err != nil {
			return nil, nil, err
		}
		ip4s = ips
	}

	if len(sub6) > 0 {
		ips, err := MemoryIPStore(sub6)
		if err != nil {
			return nil, nil, err
		}
		ip6s = ips
	}

	return ip4s, ip6s, nil
}

// MemoryIPStore returns an IPStore which allocates IP addresses from memory. The
// specified subnets must use a single IP address family: either IPv4 or IPv6
// exclusively.
func MemoryIPStore(subnets []*net.IPNet) (IPStore, error) {
	if len(subnets) == 0 {
		return nil, errors.New("wgipam: NewIPStore requires one or more subnets")
	}

	ps := make([]ipaddr.Prefix, 0, len(subnets))
	var isIPv6 bool
	for i, s := range subnets {
		// Do not allow mixed address families.
		if i == 0 {
			isIPv6 = s.IP.To4() == nil
		}

		if (isIPv6 && s.IP.To4() != nil) || (!isIPv6 && s.IP.To4() == nil) {
			return nil, errors.New("wgipam: all IPStore subnets must be the same address family")
		}

		ps = append(ps, *ipaddr.NewPrefix(s))
	}

	return &memoryIPStore{
		m: make(map[*net.IPNet]struct{}),
		c: ipaddr.NewCursor(ps),
	}, nil
}

// Allocate implements IPStore.
func (s *memoryIPStore) Allocate() (*net.IPNet, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.out {
		// No more addresses to provide.
		return nil, false, nil
	}

	p := s.c.Pos()
	if s.c.Next() == nil {
		// We've reached the end of the cursor; no more IPs.
		s.out = true
	}

	// Mark this address as allocated and return.
	ip := &net.IPNet{
		IP:   p.IP,
		Mask: p.Prefix.Mask,
	}
	s.m[ip] = struct{}{}

	return ip, true, nil
}

// Free implements IPStore.
func (s *memoryIPStore) Free(ip *net.IPNet) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.m, ip)
	return nil
}
