package wgipam

import (
	"net"
	"sync"
	"time"
)

// A Lease is a record of allocated IP addresses, assigned to a client by source
// address.
type Lease struct {
	Address net.Addr

	IPv4, IPv6 *net.IPNet
	Start      time.Time
	Length     time.Duration
}

// A LeaseStore manages Leases.
type LeaseStore interface {
	// Lease returns the Lease for source address src. It returns false if no
	// Lease exists for src.
	Lease(src net.Addr) (lease *Lease, ok bool, err error)

	// Save creates or updates a Lease.
	Save(lease *Lease) error
}

// NewLeaseStore returns a LeaseStore which stores Leases in memory.
func NewLeaseStore() LeaseStore {
	return &leaseStore{
		m: make(map[string]*Lease),
	}
}

// A leaseStore is an in-memory LeaseStore implementation.
type leaseStore struct {
	mu sync.RWMutex
	m  map[string]*Lease
}

// Lease implements LeaseStore.
func (s *leaseStore) Lease(src net.Addr) (*Lease, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	l, ok := s.m[src.String()]
	return l, ok, nil
}

// Save implements LeaseStore.
func (s *leaseStore) Save(l *Lease) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	s.m[l.Address.String()] = l
	return nil
}
