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
	_ IPAllocator = &simpleIPAllocator{}
)

// An IPAllocator can allocate IP addresses. IPAllocator implementations should
// be configured to only return IPv4 or IPv6 addresses: never both from the same
// instance.
type IPAllocator interface {
	// Allocate allocates the next available IP address. It returns false
	// if no more IP addresses are available.
	Allocate() (ip *net.IPNet, ok bool, err error)

	// Free returns an allocated IP address to the IPAllocator. Free operations
	// should be idempotent; that is, an error should only be returned if the
	// free operation fails. Attempting to free an IP that did not already exist
	// should not return an error.
	Free(ip *net.IPNet) error
}

// A simpleIPAllocator is an IPAllocator that allocates addresses in order by
// iterating through its input subnets.
type simpleIPAllocator struct {
	s       Store
	mu      sync.Mutex
	c       *ipaddr.Cursor
	subnets []net.IPNet
}

// DualStackIPAllocator returns two IPAllocators for each IPv4 and IPv6 address
// allocation. It is a convenience wrapper around NewIPAllocator that
// automatically allocates the input subnets into the appropriate IPAllocator.
func DualStackIPAllocator(store Store, subnets []net.IPNet) (ip4s, ip6s IPAllocator, err error) {
	// At least one subnet must be specified to serve.
	if len(subnets) == 0 {
		return nil, nil, errors.New("wgipam: DualStackIPAllocator must have one or more subnets to serve")
	}

	// Split subnets by family and create IPAllocators for each.
	var sub4, sub6 []net.IPNet
	for _, s := range subnets {
		if s.IP.To4() != nil {
			sub4 = append(sub4, s)
		} else {
			sub6 = append(sub6, s)
		}
	}

	if len(sub4) > 0 {
		ips, err := SimpleIPAllocator(store, sub4)
		if err != nil {
			return nil, nil, err
		}
		ip4s = ips
	}

	if len(sub6) > 0 {
		ips, err := SimpleIPAllocator(store, sub6)
		if err != nil {
			return nil, nil, err
		}
		ip6s = ips
	}

	return ip4s, ip6s, nil
}

// SimpleIPAllocator returns an IPAllocator which allocates IP addresses in order
// by iterating through its subnets. The input subnets must use a single IP
// address family: either IPv4 or IPv6 exclusively.
func SimpleIPAllocator(store Store, subnets []net.IPNet) (IPAllocator, error) {
	if len(subnets) == 0 {
		return nil, errors.New("wgipam: NewIPAllocator requires one or more subnets")
	}

	ps := make([]ipaddr.Prefix, 0, len(subnets))
	var isIPv6 bool
	for i, s := range subnets {
		// Do not allow mixed address families.
		if i == 0 {
			isIPv6 = s.IP.To4() == nil
		}

		if (isIPv6 && s.IP.To4() != nil) || (!isIPv6 && s.IP.To4() == nil) {
			return nil, errors.New("wgipam: all IPAllocator subnets must be the same address family")
		}

		// Capture the range variable to get a unique pointer and register this
		// subnet with our Store for later use.
		s := s
		ps = append(ps, *ipaddr.NewPrefix(&s))
		if err := store.SaveSubnet(&s); err != nil {
			return nil, err
		}
	}

	return &simpleIPAllocator{
		s:       store,
		c:       ipaddr.NewCursor(ps),
		subnets: subnets,
	}, nil
}

// Allocate implements IPAllocator.
func (s *simpleIPAllocator) Allocate() (*net.IPNet, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out bool
	for {
		p := s.c.Pos()
		if s.c.Next() == nil {
			if out {
				// No more addresses to provide.
				return nil, false, nil
			}

			// We've reached the end of the cursor, seek back to the beginning.
			if err := s.c.Set(s.c.First()); err != nil {
				return nil, false, err
			}
			out = true
		}

		// Try to allocate this address. If unsuccessful, the loop will continue
		// until we reach a free address or run out of IPs.
		ip := &net.IPNet{
			IP:   p.IP,
			Mask: p.Prefix.Mask,
		}

		ok, err := s.s.AllocateIP(&p.Prefix.IPNet, ip)
		if err != nil {
			return nil, false, err
		}
		if ok {
			// Address successfully allocated.
			return ip, true, nil
		}
	}
}

// Free implements IPAllocator.
func (s *simpleIPAllocator) Free(ip *net.IPNet) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, sub := range s.subnets {
		if !sub.Contains(ip.IP) {
			continue
		}

		if err := s.s.FreeIP(&sub, ip); err != nil {
			return err
		}
	}

	return nil
}
