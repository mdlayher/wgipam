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
	"io"
	"net"
	"sync"
	"time"
)

var (
	_ LeaseStore = &memoryLeaseStore{}
)

// A Lease is a record of allocated IP addresses, assigned to a client by a Key
// (typically a source address).
type Lease struct {
	Key string

	IPv4, IPv6 *net.IPNet
	Start      time.Time
	Length     time.Duration
}

// String returns a string suitable for logging.
func (l *Lease) String() string {
	// Address is handled in server logs, so omit it here.
	return fmt.Sprintf("IPv4: %s, IPv6: %s, start: %s, end: %s",
		l.IPv4, l.IPv6,
		// time.Stamp seems to be reasonably readable.
		l.Start.Format(time.Stamp),
		l.Start.Add(l.Length).Format(time.Stamp),
	)
}

// A LeaseStore manages Leases. To ensure compliance with the expected behaviors
// of the LeaseStore interface, use the wgipamtest.TestLeaseStore function.
type LeaseStore interface {
	// Close syncs and closes the LeaseStore's internal state.
	io.Closer

	// Leases returns all existing Leases.
	Leases() (leases []*Lease, err error)

	// Lease returns the Lease identified by key. It returns false if no
	// Lease exists for key.
	Lease(key string) (lease *Lease, ok bool, err error)

	// Save creates or updates a Lease.
	Save(lease *Lease) error

	// Delete deletes a Lease.
	Delete(lease *Lease) error
}

// A memoryLeaseStore is an in-memory LeaseStore implementation.
type memoryLeaseStore struct {
	mu sync.RWMutex
	m  map[string]*Lease
}

// MemoryLeaseStore returns a LeaseStore which stores Leases in memory.
func MemoryLeaseStore() LeaseStore {
	return &memoryLeaseStore{
		m: make(map[string]*Lease),
	}
}

// Close implements LeaseStore.
func (s *memoryLeaseStore) Close() error { return nil }

// Leases implements LeaseStore.
func (s *memoryLeaseStore) Leases() ([]*Lease, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ls := make([]*Lease, 0, len(s.m))
	for _, l := range s.m {
		ls = append(ls, l)
	}

	return ls, nil
}

// Lease implements LeaseStore.
func (s *memoryLeaseStore) Lease(key string) (*Lease, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	l, ok := s.m[key]
	return l, ok, nil
}

// Save implements LeaseStore.
func (s *memoryLeaseStore) Save(l *Lease) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.m[l.Key] = l
	return nil
}

// Delete implements LeaseStore.
func (s *memoryLeaseStore) Delete(l *Lease) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Verify the lease was actually allocated.
	if _, ok := s.m[l.Key]; !ok {
		return fmt.Errorf("wgipam: no lease for client %q", l.Key)
	}

	delete(s.m, l.Key)
	return nil
}
