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

// An IPStore can allocate IP addresses. IPStore implementations should be
// configured to only return IPv4 or IPv6 addresses: never both from the same
// instance.
type IPStore interface {
	// Allocate allocates the next available IP address. It returns false
	// if no more IP addresses are available.
	Allocate() (ip *net.IPNet, ok bool, err error)
}

var _ IPStore = &ipStore{}

// An ipStore is an in-memory IPStore implementation.
type ipStore struct {
	mu  sync.Mutex
	c   *ipaddr.Cursor
	out bool
}

// NewIPStore returns an IPStore which allocates IP addresses from memory.
func NewIPStore(subnets []*net.IPNet) (IPStore, error) {
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

	return &ipStore{
		c: ipaddr.NewCursor(ps),
	}, nil
}

// Allocate implements IPStore.
func (s *ipStore) Allocate() (*net.IPNet, bool, error) {
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

	// TODO(mdlayher): track the allocation in a map.

	return &net.IPNet{
		IP:   p.IP,
		Mask: p.Prefix.Mask,
	}, true, nil
}
