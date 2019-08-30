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
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"go.etcd.io/bbolt"
)

var (
	_ LeaseStore = &memoryLeaseStore{}
	_ LeaseStore = &boltLeaseStore{}
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

// TODO(mdlayher): JSON is quick and easy but it's probably best to build out
// a binary format.

// marshal marshals a Lease to binary form.
func (l *Lease) marshal() ([]byte, error) {
	return json.Marshal(l)
}

// unmarshal unmarshals a Lease from binary form.
func (l *Lease) unmarshal(b []byte) error {
	return json.Unmarshal(b, l)
}

// A LeaseStore manages Leases. To ensure compliance with the expected behaviors
// of the LeaseStore interface, use the wgipamtest.TestLeaseStore function.
type LeaseStore interface {
	// Close syncs and closes the LeaseStore's internal state.
	io.Closer

	// Leases returns all existing Leases. Note that the order of the Leases
	// is unspecified. The caller must sort the leases for deterministic output.
	Leases() (leases []*Lease, err error)

	// Lease returns the Lease identified by key. It returns false if no
	// Lease exists for key.
	Lease(key string) (lease *Lease, ok bool, err error)

	// Save creates or updates a Lease.
	Save(lease *Lease) error

	// Delete deletes a Lease. Delete operations should be idempotent; that is,
	// an error should only be returned if the delete operation fails.
	// Attempting to delete an item that did not already exist should not
	// return an error.
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

	// Return order is unspecified, so map iteration is no problem.
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

	delete(s.m, l.Key)
	return nil
}

// Bolt database bucket names.
var (
	bucketLeases = []byte("leases")
)

// A leaseStore is an in-memory LeaseStore implementation.
type boltLeaseStore struct {
	db *bbolt.DB
}

// FileLeaseStore returns a LeaseStore which stores Leases in a file on disk.
func FileLeaseStore(file string) (LeaseStore, error) {
	// The file store uses bolt, but this is considered an implementation
	// detail and there's no need to expose this as BoltLeaseStore or similar.
	db, err := bbolt.Open(file, 0644, &bbolt.Options{
		Timeout: 5 * time.Second,
	})
	if err != nil {
		return nil, err
	}

	err = db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucketLeases)
		return err
	})
	if err != nil {
		return nil, err
	}

	return &boltLeaseStore{
		db: db,
	}, nil
}

// Close implements LeaseStore.
func (s *boltLeaseStore) Close() error { return s.db.Close() }

// Leases implements LeaseStore.
func (s *boltLeaseStore) Leases() ([]*Lease, error) {
	var leases []*Lease

	err := s.db.View(func(tx *bbolt.Tx) error {
		// Unmarshal each Lease from its bucket.
		return tx.Bucket(bucketLeases).ForEach(func(_ []byte, v []byte) error {
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

// Lease implements LeaseStore.
func (s *boltLeaseStore) Lease(key string) (*Lease, bool, error) {
	var l *Lease
	err := s.db.View(func(tx *bbolt.Tx) error {
		v := tx.Bucket(bucketLeases).Get([]byte(key))
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

// Save implements LeaseStore.
func (s *boltLeaseStore) Save(l *Lease) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		lb, err := l.marshal()
		if err != nil {
			return err
		}

		return tx.Bucket(bucketLeases).Put([]byte(l.Key), lb)
	})
}

// Delete implements LeaseStore.
func (s *boltLeaseStore) Delete(l *Lease) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket(bucketLeases).Delete([]byte(l.Key))
	})
}
