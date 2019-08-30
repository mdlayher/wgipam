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
	"io"
	"sync"
	"time"

	"github.com/zeebo/xxh3"
	"go.etcd.io/bbolt"
)

var (
	_ Store = &memoryStore{}
	_ Store = &boltStore{}
)

// A Store manages Leases. To ensure compliance with the expected behaviors
// of the Store interface, use the wgipamtest.TestStore function.
type Store interface {
	// Close syncs and closes the Store's internal state.
	io.Closer

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

	// Purge purge Leases which expire on or before the specified point in time.
	// Purge operations that specify the same point in time should be
	// idempotent; the same rules apply as with Delete.
	Purge(t time.Time) error
}

// A memoryStore is an in-memory Store implementation.
type memoryStore struct {
	mu sync.RWMutex
	m  map[uint64]*Lease
}

// MemoryStore returns a Store which stores Leases in memory.
func MemoryStore() Store {
	return &memoryStore{
		m: make(map[uint64]*Lease),
	}
}

// Close implements Store.
func (s *memoryStore) Close() error { return nil }

// Leases implements Store.
func (s *memoryStore) Leases() ([]*Lease, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Return order is unspecified, so map iteration is no problem.
	ls := make([]*Lease, 0, len(s.m))
	for _, l := range s.m {
		ls = append(ls, l)
	}

	return ls, nil
}

// Lease implements Store.
func (s *memoryStore) Lease(key uint64) (*Lease, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	l, ok := s.m[key]
	return l, ok, nil
}

// SaveLease implements Store.
func (s *memoryStore) SaveLease(key uint64, l *Lease) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.m[key] = l
	return nil
}

// DeleteLease implements Store.
func (s *memoryStore) DeleteLease(key uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.m, key)
	return nil
}

// Purge implements Store.
func (s *memoryStore) Purge(t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Deleting from a map during iteration is okay:
	// https://golang.org/doc/effective_go.html#for.
	for k, v := range s.m {
		if v.Expired(t) {
			delete(s.m, k)
		}
	}

	return nil
}

// Bolt database bucket names.
var (
	bucketLeases = []byte("leases")
)

// A leaseStore is an in-memory Store implementation.
type boltStore struct {
	db *bbolt.DB
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
		_, err := tx.CreateBucketIfNotExists(bucketLeases)
		return err
	})
	if err != nil {
		return nil, err
	}

	return &boltStore{
		db: db,
	}, nil
}

// Close implements Store.
func (s *boltStore) Close() error { return s.db.Close() }

// Leases implements Store.
func (s *boltStore) Leases() ([]*Lease, error) {
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
func (s *boltStore) Purge(t time.Time) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		// Track keys for removal after iteration completes.
		var keys [][]byte

		b := tx.Bucket(bucketLeases)
		err := b.ForEach(func(k []byte, v []byte) error {
			var l Lease
			if err := l.unmarshal(v); err != nil {
				return err
			}

			if l.Expired(t) {
				keys = append(keys, k)
			}

			return nil
		})
		if err != nil {
			return err
		}

		for _, k := range keys {
			if err := b.Delete(k); err != nil {
				return err
			}
		}

		return nil
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
