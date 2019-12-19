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
	"net"
	"sync"

	"github.com/mikioh/ipaddr"
)

var (
	_ IPAllocator = &multiIPAllocator{}
	_ IPAllocator = &simpleIPAllocator{}
)

// A Family specifies one or more IP address families, such as IPv4, IPv6,
// or DualStack.
type Family int

//go:generate stringer -type=Family -output=string.go

// List of possible Family values.
const (
	_ Family = iota
	IPv4
	IPv6
	DualStack
)

// An IPAllocator can allocate IP addresses from one or more subnets.
type IPAllocator interface {
	// Allocate allocates an available IP address from each underlying subnet
	// which matches the input Family. It returns false if one or more of the
	// subnets ran out of IP addresses during allocation.
	Allocate(family Family) (ips []*net.IPNet, ok bool, err error)

	// Free returns an allocated IP address to the IPAllocator. Free operations
	// should be idempotent; that is, an error should only be returned if the
	// free operation fails. Attempting to free an IP that did not already exist
	// should not return an error.
	Free(ip *net.IPNet) error
}

// A multiIPAllocator is an IPAllocator that wraps several internal IPAllocators,
// so different allocations strategies may be used for different IP families.
type multiIPAllocator struct {
	s Store
	m map[Family][]IPAllocator
}

// DualStackIPAllocator returns an IPAllocator which can allocate both IPv4 and
// IPv6 addresses. It is a convenience wrapper around other IPAllocators that
// automatically allocates addresses from the input subnets as appropriate.
func DualStackIPAllocator(store Store, subnets []net.IPNet) (IPAllocator, error) {
	// At least one subnet must be specified to serve.
	if len(subnets) == 0 {
		return nil, errors.New("wgipam: DualStackIPAllocator must have one or more subnets to serve")
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

	mia := &multiIPAllocator{
		s: store,
		m: make(map[Family][]IPAllocator),
	}

	// Each subnet gets its own IPAllocator.

	for _, s := range sub4 {
		ipa, err := SimpleIPAllocator(store, s)
		if err != nil {
			return nil, err
		}

		mia.m[IPv4] = append(mia.m[IPv4], ipa)
	}

	for _, s := range sub6 {
		ipa, err := SimpleIPAllocator(store, s)
		if err != nil {
			return nil, err
		}

		mia.m[IPv6] = append(mia.m[IPv6], ipa)
	}

	return mia, nil
}

// An ipaPair is a tuple of Family and IPAllocator.
type ipaPair struct {
	f  Family
	as []IPAllocator
}

// Allocate implements IPAllocator.
func (mia *multiIPAllocator) Allocate(family Family) ([]*net.IPNet, bool, error) {
	// Determine which IPAllocators should be consulted based on the input
	// Family value.
	var pairs []ipaPair
	switch family {
	case IPv4, IPv6:
		pairs = append(pairs, ipaPair{
			f:  family,
			as: mia.m[family],
		})
	case DualStack:
		pairs = append(pairs, ipaPair{
			f:  IPv4,
			as: mia.m[IPv4],
		})
		pairs = append(pairs, ipaPair{
			f:  IPv6,
			as: mia.m[IPv6],
		})
	default:
		panicf("wgipam: invalid IP Family value: %#v", family)
	}

	ips, ok, err := mia.tryAllocate(pairs)
	if err != nil || !ok {
		// Allocation failed due to error or running out of addresses, but
		// tryAllocate returns whatever addresses it allocated along the way
		// so we can free them now.
		for _, ip := range ips {
			if ferr := mia.Free(ip); ferr != nil {
				return nil, false, fmt.Errorf("failed to free IP address %s: %v, original error: %v", ip, ferr, err)
			}
		}

		return nil, ok, err
	}

	return ips, true, nil
}

// tryAllocate attempts to allocate addresses using the given ipaPairs, returning
// any addresses it was able to allocate along with any errors.
func (mia *multiIPAllocator) tryAllocate(pairs []ipaPair) ([]*net.IPNet, bool, error) {
	// All returns in this function _must_ return out for cleanup to work.
	var out []*net.IPNet

	for _, p := range pairs {
		for _, ipa := range p.as {
			ips, ok, err := ipa.Allocate(p.f)
			if err != nil {
				return out, false, err
			}
			if !ok {
				return out, false, nil
			}

			out = append(out, ips...)
		}
	}

	if len(out) == 0 {
		// Nothing allocated, out of addresses.
		return out, false, nil
	}

	return out, true, nil
}

// Free implements IPAllocator.
func (mia *multiIPAllocator) Free(ip *net.IPNet) error {
	// Delegate directly to the appropriate Family's IPAllocators and try to
	// remove the address from each subnet.
	for _, ipa := range mia.m[ipFamily(ip)] {
		if err := ipa.Free(ip); err != nil {
			return err
		}
	}

	return nil
}

// A simpleIPAllocator is an IPAllocator that allocates addresses in order by
// iterating through its input subnets.
type simpleIPAllocator struct {
	f      Family
	s      Store
	mu     sync.Mutex
	c      *ipaddr.Cursor
	subnet *net.IPNet
}

// SimpleIPAllocator returns an IPAllocator which allocates IP addresses in order
// by iterating through addresses in a subnet.
func SimpleIPAllocator(store Store, subnet net.IPNet) (IPAllocator, error) {
	if err := store.SaveSubnet(&subnet); err != nil {
		return nil, err
	}

	return &simpleIPAllocator{
		f:      ipFamily(&subnet),
		s:      store,
		c:      ipaddr.NewCursor([]ipaddr.Prefix{*ipaddr.NewPrefix(&subnet)}),
		subnet: &subnet,
	}, nil
}

// Allocate implements IPAllocator.
func (s *simpleIPAllocator) Allocate(family Family) ([]*net.IPNet, bool, error) {
	if family != s.f {
		return nil, false, fmt.Errorf("wgipam: IPAllocator for subnet %s only manages %s addresses", s.subnet, s.f)
	}

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
			Mask: ipMask(&p.Prefix.IPNet),
		}

		ok, err := s.s.AllocateIP(&p.Prefix.IPNet, ip)
		if err != nil {
			return nil, false, err
		}
		if ok {
			// Address successfully allocated.
			return []*net.IPNet{ip}, true, nil
		}
	}
}

// Free implements IPAllocator.
func (s *simpleIPAllocator) Free(ip *net.IPNet) error {
	if !s.subnet.Contains(ip.IP) {
		return nil
	}

	return s.s.FreeIP(s.subnet, ip)
}

func ipMask(ip *net.IPNet) net.IPMask {
	switch f := ipFamily(ip); f {
	case IPv4:
		return net.CIDRMask(32, 32)
	case IPv6:
		return net.CIDRMask(128, 128)
	default:
		panicf("wgipam: invalid family for %s IP mask: %s", ip, f)
	}

	panic("unreachable")
}

// ipFamily returns the Family value for ip.
func ipFamily(ip *net.IPNet) Family {
	switch {
	case ip.IP.To16() != nil && ip.IP.To4() != nil:
		return IPv4
	case ip.IP.To16() != nil && ip.IP.To4() == nil:
		return IPv6
	default:
		panicf("wgipam: invalid IP address: %v", ip)
	}

	panic("unreachable")
}
