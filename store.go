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
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/zeebo/xxh3"
	"go.etcd.io/bbolt"
)

var (
	_ Store = &memoryStore{}
	_ Store = &boltStore{}
)

// TODO: tidy further when overlapping interfaces are allowed in Go 1.14.

// A Store manages Leases and IP allocations. To ensure compliance with the
// expected behaviors of the Store interface, use the wgipamtest.TestStore
// function.
type Store interface {
	// Close syncs and closes the Store's internal state.
	io.Closer

	// Purge purge Leases which expire on or before the specified point in time,
	// and frees the IP addresses associated with the leases.
	// Purge operations that specify the same point in time should be
	// idempotent; the same rules apply as with DeleteLease.
	Purge(t time.Time) (*PurgeStats, error)

	LeaseStore
	IPStore
}

// A LeaseStore manages Lease storage.
type LeaseStore interface {
	// Leases returns all existing Leases. Note that the order of the Leases
	// is unspecified. The caller must sort the leases for deterministic output.
	Leases() (leases []*Lease, err error)

	// Lease returns the Lease identified by key. It returns false if no
	// Lease exists for key.
	Lease(key uint64) (lease *Lease, ok bool, err error)

	// SaveLease creates or updates a Lease by key.
	SaveLease(key uint64, lease *Lease) error

	// DeleteLease deletes a Lease by key. Delete operations should be idempotent;
	// that is, an error should only be returned if the delete operation fails.
	// Attempting to delete an item that did not already exist should not
	// return an error.
	DeleteLease(key uint64) error
}

// An IPStore manages IP address allocation and storage.
type IPStore interface {
	// Subnets returns all existing subnets. Note that the order of the subnets
	// is unspecified. The caller must sort the leases for deterministic output.
	Subnets() (subnets []*net.IPNet, err error)

	// SaveSubnet creates or updates a Subnet by its CIDR value.
	SaveSubnet(subnet *net.IPNet) error

	// AllocatedIPs returns all of the IP addresses allocated within a specific
	// subnet. Note that the order of the addresses is unspecified. The caller
	// must sort the addresses for deterministic output.
	AllocatedIPs(subnet *net.IPNet) (ips []*net.IPNet, err error)

	// AllocateIP allocates an IP address from the specified subnet. It returns
	// false if the address is already allocated from the subnet, and returns an
	// error if the subnet does not exist.
	AllocateIP(subnet, ip *net.IPNet) (ok bool, err error)

	// FreeIP frees an allocated IP address in the specified subnet. FreeIP
	// operations should be idempotent; the same rules apply as with DeleteLease.
	// It returns an error if the subnet does not exist.
	FreeIP(subnet, ip *net.IPNet) error
}

// PurgeStats contains statistics returned from a Store's Purge operation.
type PurgeStats struct {
	// FreedIPs contains a map of subnet CIDRs to the number of IP addresses
	// that were freed within that subnet.
	FreedIPs map[string]int
}

// A memoryStore is an in-memory Store implementation.
type memoryStore struct {
	leasesMu sync.RWMutex
	leases   map[uint64]*Lease

	subnetsMu sync.RWMutex
	subnets   map[string]map[string]struct{}
}

// MemoryStore returns a Store which stores data in memory.
func MemoryStore() Store {
	return &memoryStore{
		leases:  make(map[uint64]*Lease),
		subnets: make(map[string]map[string]struct{}),
	}
}

// Close implements Store.
func (s *memoryStore) Close() error { return nil }

// Leases implements Store.
func (s *memoryStore) Leases() ([]*Lease, error) {
	s.leasesMu.RLock()
	defer s.leasesMu.RUnlock()

	// Return order is unspecified, so map iteration is no problem.
	ls := make([]*Lease, 0, len(s.leases))
	for _, l := range s.leases {
		ls = append(ls, l)
	}

	return ls, nil
}

// Lease implements Store.
func (s *memoryStore) Lease(key uint64) (*Lease, bool, error) {
	s.leasesMu.RLock()
	defer s.leasesMu.RUnlock()

	l, ok := s.leases[key]
	return l, ok, nil
}

// SaveLease implements Store.
func (s *memoryStore) SaveLease(key uint64, l *Lease) error {
	s.leasesMu.Lock()
	defer s.leasesMu.Unlock()

	s.leases[key] = l
	return nil
}

// DeleteLease implements Store.
func (s *memoryStore) DeleteLease(key uint64) error {
	s.leasesMu.Lock()
	defer s.leasesMu.Unlock()

	delete(s.leases, key)
	return nil
}

// Purge implements Store.
func (s *memoryStore) Purge(t time.Time) (*PurgeStats, error) {
	ps := PurgeStats{
		FreedIPs: make(map[string]int),
	}

	s.leasesMu.Lock()
	defer s.leasesMu.Unlock()

	// Deleting from a map during iteration is okay:
	// https://golang.org/doc/effective_go.html#for.

	// Track the IP addresses that belong to expired leases.
	var freed []*net.IPNet
	for k, v := range s.leases {
		if !v.Expired(t) {
			continue
		}

		delete(s.leases, k)
		freed = append(freed, v.IPs...)
	}

	s.subnetsMu.Lock()
	defer s.subnetsMu.Unlock()

	// And sweep through each subnet, freeing IP addresses whose leases have
	// expired.
	for sub := range s.subnets {
		ip, cidr, err := net.ParseCIDR(sub)
		if err != nil {
			return nil, err
		}
		cidr.IP = ip

		for _, f := range freed {
			if cidr.Contains(f.IP) {
				ps.FreedIPs[sub]++
				delete(s.subnets[sub], f.String())
			}
		}
	}

	return &ps, nil
}

// Subnets implements Store.
func (s *memoryStore) Subnets() ([]*net.IPNet, error) {
	s.subnetsMu.RLock()
	defer s.subnetsMu.RUnlock()

	// Return order is unspecified, so map iteration is no problem.
	ss := make([]*net.IPNet, 0, len(s.subnets))
	for sub := range s.subnets {
		_, cidr, err := net.ParseCIDR(sub)
		if err != nil {
			return nil, err
		}

		ss = append(ss, cidr)
	}

	return ss, nil
}

// SaveSubnet implements Store.
func (s *memoryStore) SaveSubnet(sub *net.IPNet) error {
	s.subnetsMu.Lock()
	defer s.subnetsMu.Unlock()

	s.subnets[sub.String()] = make(map[string]struct{})
	return nil
}

// AllocatedIPs implements Store.
func (s *memoryStore) AllocatedIPs(sub *net.IPNet) ([]*net.IPNet, error) {
	s.subnetsMu.RLock()
	defer s.subnetsMu.RUnlock()

	subS := sub.String()
	if _, ok := s.subnets[subS]; !ok {
		return nil, fmt.Errorf("wgipam: no such subnet %q", subS)
	}

	ips := make([]*net.IPNet, 0, len(s.subnets[subS]))
	for k := range s.subnets[subS] {
		ip, cidr, err := net.ParseCIDR(k)
		if err != nil {
			return nil, err
		}
		cidr.IP = ip

		ips = append(ips, cidr)
	}

	return ips, nil
}

// AllocateIP implements Store.
func (s *memoryStore) AllocateIP(sub, ip *net.IPNet) (bool, error) {
	if err := checkSubnetContains(sub, ip); err != nil {
		return false, err
	}

	s.subnetsMu.Lock()
	defer s.subnetsMu.Unlock()

	subS, ipS := sub.String(), ip.String()
	if _, ok := s.subnets[subS]; !ok {
		return false, fmt.Errorf("wgipam: no such subnet %q", subS)
	}
	if _, ok := s.subnets[subS][ipS]; ok {
		// Already allocated.
		return false, nil
	}

	// New address allocated.
	s.subnets[subS][ipS] = struct{}{}
	return true, nil
}

// FreeIP implements Store.
func (s *memoryStore) FreeIP(sub, ip *net.IPNet) error {
	if err := checkSubnetContains(sub, ip); err != nil {
		return err
	}

	s.subnetsMu.Lock()
	defer s.subnetsMu.Unlock()

	subS, ipS := sub.String(), ip.String()
	if _, ok := s.subnets[subS]; !ok {
		return fmt.Errorf("wgipam: no such subnet %q", subS)
	}

	delete(s.subnets[subS], ipS)
	return nil
}

// Bolt database bucket names.
var (
	bucketLeases  = []byte("leases")
	bucketSubnets = []byte("subnets")
)

// A leaseStore is an in-memory Store implementation.
type boltStore struct {
	db *bbolt.DB

	mu      sync.RWMutex
	subnets map[string]*net.IPNet
}

// FileStore returns a Store which stores Leases in a file on disk.
func FileStore(file string) (Store, error) {
	// The file store uses bolt, but this is considered an implementation
	// detail and there's no need to expose this as BoltStore or similar.
	db, err := bbolt.Open(file, 0644, &bbolt.Options{
		Timeout: 5 * time.Second,
	})
	if err != nil {
		return nil, err
	}

	err = db.Update(func(tx *bbolt.Tx) error {
		// Create all required buckets.
		for _, b := range [][]byte{bucketLeases, bucketSubnets} {
			if _, err := tx.CreateBucketIfNotExists(b); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return &boltStore{
		db:      db,
		subnets: make(map[string]*net.IPNet),
	}, nil
}

// Close implements Store.
func (s *boltStore) Close() error { return s.db.Close() }

// Leases implements Store.
func (s *boltStore) Leases() ([]*Lease, error) {
	var leases []*Lease

	err := s.db.View(func(tx *bbolt.Tx) error {
		// Unmarshal each Lease from its bucket.
		return tx.Bucket(bucketLeases).ForEach(func(_, v []byte) error {
			var l Lease
			if err := l.unmarshal(v); err != nil {
				return err
			}

			leases = append(leases, &l)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	return leases, nil
}

// Lease implements Store.
func (s *boltStore) Lease(key uint64) (*Lease, bool, error) {
	var l *Lease
	err := s.db.View(func(tx *bbolt.Tx) error {
		v := tx.Bucket(bucketLeases).Get(keyBytes(key))
		if v == nil {
			// No lease found, do not populate l.
			return nil
		}

		l = &Lease{}
		return l.unmarshal(v)
	})
	if err != nil {
		return nil, false, err
	}

	if l == nil {
		// No lease found.
		return nil, false, nil
	}

	// Lease found.
	return l, true, nil
}

// SaveLease implements Store.
func (s *boltStore) SaveLease(key uint64, l *Lease) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		lb, err := l.marshal()
		if err != nil {
			return err
		}

		return tx.Bucket(bucketLeases).Put(keyBytes(key), lb)
	})
}

// DeleteLease implements Store.
func (s *boltStore) DeleteLease(key uint64) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket(bucketLeases).Delete(keyBytes(key))
	})
}

// Purge implements Store.
func (s *boltStore) Purge(t time.Time) (*PurgeStats, error) {
	ps := PurgeStats{
		FreedIPs: make(map[string]int),
	}

	err := s.db.Update(func(tx *bbolt.Tx) error {
		// Track lease keys and IP addresses for removal after iteration
		// completes.
		var (
			leases [][]byte
			freed  []*net.IPNet
		)

		bLeases := tx.Bucket(bucketLeases)
		err := bLeases.ForEach(func(k, v []byte) error {
			var l Lease
			if err := l.unmarshal(v); err != nil {
				return err
			}

			if !l.Expired(t) {
				return nil
			}

			leases = append(leases, k)
			freed = append(freed, l.IPs...)

			return nil
		})
		if err != nil {
			return err
		}

		for _, l := range leases {
			if err := bLeases.Delete(l); err != nil {
				return err
			}
		}

		// Now we must clear the associated addresses from the subnets bucket.
		// Because the ForEach API cannot modify the bucket, we keep a separate
		// list of subnets and iterate through each of their buckets to remove
		// the IP keys that belong to those buckets.
		//
		// This could use some work, but it seems to work for now.
		s.mu.RLock()
		defer s.mu.RUnlock()

		bSubnets := tx.Bucket(bucketSubnets)
		for _, sub := range s.subnets {
			bSub := marshalIPNet(sub)
			for _, f := range freed {
				if sub.Contains(f.IP) {
					ps.FreedIPs[sub.String()]++

					if err := bSubnets.Bucket(bSub).Delete(marshalIPNet(f)); err != nil {
						return err
					}
				}
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return &ps, nil
}

// Subnets implements Store.
func (s *boltStore) Subnets() ([]*net.IPNet, error) {
	var subnets []*net.IPNet

	err := s.db.View(func(tx *bbolt.Tx) error {
		// Unmarshal each subnet from its bucket.
		return tx.Bucket(bucketSubnets).ForEach(func(k, _ []byte) error {
			sub, err := unmarshalIPNet(k)
			if err != nil {
				return err
			}

			subnets = append(subnets, sub)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	return subnets, nil
}

// SaveSubnet implements Store.
func (s *boltStore) SaveSubnet(sub *net.IPNet) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.subnets[sub.String()] = sub

	return s.db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.Bucket(bucketSubnets).CreateBucketIfNotExists(marshalIPNet(sub))
		return err
	})
}

// AllocatedIPs implements Store.
func (s *boltStore) AllocatedIPs(sub *net.IPNet) ([]*net.IPNet, error) {
	var ips []*net.IPNet

	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketSubnets).Bucket(marshalIPNet(sub))
		if b == nil {
			return fmt.Errorf("wgipam: no such subnet %q", sub)
		}

		return b.ForEach(func(k, _ []byte) error {
			ip, err := unmarshalIPNet(k)
			if err != nil {
				return err
			}

			ips = append(ips, ip)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	return ips, nil
}

// SaveSubnet implements Store.
func (s *boltStore) AllocateIP(sub, ip *net.IPNet) (bool, error) {
	if err := checkSubnetContains(sub, ip); err != nil {
		return false, err
	}

	// Was the address allocated?
	var ok bool
	err := s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketSubnets).Bucket(marshalIPNet(sub))
		if b == nil {
			return fmt.Errorf("wgipam: no such subnet %q", sub)
		}

		k := marshalIPNet(ip)
		if b.Get(k) != nil {
			// Address already exists, return now and don't allocate anything.
			return nil
		}

		// We can allocate this address, set ok to true.
		//
		// For now we don't store any data, we just ensure a key exists so that
		// other callers can't reserve the same address.
		ok = true
		return b.Put(k, []byte{})
	})
	if err != nil {
		return false, err
	}

	return ok, nil
}

// FreeIP implements Store.
func (s *boltStore) FreeIP(sub, ip *net.IPNet) error {
	if err := checkSubnetContains(sub, ip); err != nil {
		return err
	}

	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketSubnets).Bucket(marshalIPNet(sub))
		if b == nil {
			return fmt.Errorf("wgipam: no such subnet %q", sub)
		}

		return b.Delete(marshalIPNet(ip))
	})
}

// timeNow returns the current time with a 1 second granularity.
func timeNow() time.Time {
	// There's no point in using extremely high resolution time in this service,
	// so round everything to the nearest second.
	return time.Unix(int64(time.Now().Unix()), 0)
}

// strKey hashes s into a key.
func strKey(s string) uint64 {
	// Must be kept in sync with wgipam_test.strKey as well.
	return xxh3.HashString(s)
}

// keyBytes converts k into a key for use with bolt.
func keyBytes(k uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, k)
	return b
}

// checkSubnetContains verifies that ip resides within sub.
func checkSubnetContains(sub, ip *net.IPNet) error {
	if !sub.Contains(ip.IP) {
		return fmt.Errorf("wgipam: subnet %q cannot contain IP %q", sub, ip)
	}

	return nil
}

// TODO(mdlayher): smarter marshaling and unmarshaling logic.

// marshalIPNet marshals a net.IPNet into binary form.
func marshalIPNet(n *net.IPNet) []byte {
	return []byte(n.String())
}

// unmarshalIPNet unmarshals a net.IPNet from binary form.
func unmarshalIPNet(b []byte) (*net.IPNet, error) {
	ip, cidr, err := net.ParseCIDR(string(b))
	if err != nil {
		return nil, err
	}

	// TODO(mdlayher): figure out if we're retaining the subnet's mask or
	// doing /32 and /128.
	cidr.IP = ip

	return cidr, nil
}
