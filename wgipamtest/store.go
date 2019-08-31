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

package wgipamtest

import (
	"fmt"
	"net"
	"sort"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/wgipam"
)

var (
	// okLease is a lease which is fully populated and can be used in tests.
	okLease = &wgipam.Lease{
		IPv4:   mustCIDR("192.0.2.0/32"),
		IPv6:   mustCIDR("2001:db8::/128"),
		Start:  time.Unix(1, 0),
		Length: 10 * time.Second,
	}
)

// MakeStore is a function which produces a new wgipam.Store on each
// invocation. The Store should be completely empty when created.
type MakeStore func(t *testing.T) wgipam.Store

// TestStore tests a wgipam.Store type for compliance with the
// interface. The MakeStore function is invoked to retrieve a new and empty
// Store for each subtest.
func TestStore(t *testing.T, ms MakeStore) {
	t.Helper()

	tests := []struct {
		name string
		fn   func(t *testing.T, s wgipam.Store)
	}{
		{
			name: "leases empty",
			fn:   testLeasesEmpty,
		},
		{
			name: "leases OK",
			fn:   testLeasesOK,
		},
		{
			name: "lease not exist",
			fn:   testLeaseNotExist,
		},
		{
			name: "save lease OK",
			fn:   testSaveLeaseOK,
		},
		{
			name: "delete lease OK",
			fn:   testDeleteLeaseOK,
		},
		{
			name: "purge lease OK",
			fn:   testPurgeLeaseOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := ms(t)
			defer s.Close()
			tt.fn(t, s)
		})
	}
}

func testLeasesEmpty(t *testing.T, s wgipam.Store) {
	t.Helper()

	leases, err := s.Leases()
	if err != nil {
		t.Fatalf("failed to get leases: %v", err)
	}
	if diff := cmp.Diff(0, len(leases)); diff != "" {
		t.Fatalf("unexpected number of leases (-want +got):\n%s", diff)
	}
}

func testLeasesOK(t *testing.T, s wgipam.Store) {
	t.Helper()

	// Save some synthetic leases to be fetched again later.
	for i := 0; i < 3; i++ {
		if err := s.SaveLease(uint64(i), okLease); err != nil {
			t.Fatalf("failed to save lease: %v", err)
		}
	}

	got, err := s.Leases()
	if err != nil {
		t.Fatalf("failed to get leases: %v", err)
	}

	// No ordering guarantees are made, so sort both slices for comparison.
	want := []*wgipam.Lease{
		okLease, okLease, okLease,
	}

	sort.SliceStable(want, func(i, j int) bool {
		return want[i].Start.Before(want[j].Start)
	})
	sort.SliceStable(got, func(i, j int) bool {
		return got[i].Start.Before(got[j].Start)
	})

	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("unexpected Leases (-want +got):\n%s", diff)
	}
}

func testLeaseNotExist(t *testing.T, s wgipam.Store) {
	t.Helper()

	l, ok, err := s.Lease(1)
	if err != nil {
		t.Fatalf("failed to get lease: %v", err)
	}
	if ok {
		t.Fatal("found a lease when none was expected")
	}
	if l != nil {
		t.Fatal("returned non-nil lease when not found")
	}
}

func testSaveLeaseOK(t *testing.T, s wgipam.Store) {
	t.Helper()

	const key = 1
	if err := s.SaveLease(key, okLease); err != nil {
		t.Fatalf("failed to save lease: %v", err)
	}

	l, ok, err := s.Lease(key)
	if err != nil {
		t.Fatalf("failed to get lease: %v", err)
	}
	if !ok {
		t.Fatal("expected a lease but one was not found")
	}

	if diff := cmp.Diff(okLease, l); diff != "" {
		t.Fatalf("unexpected Lease (-want +got):\n%s", diff)
	}
}

func testDeleteLeaseOK(t *testing.T, s wgipam.Store) {
	t.Helper()

	const key = 1
	if err := s.SaveLease(key, okLease); err != nil {
		t.Fatalf("failed to save lease: %v", err)
	}

	// Repeated deletions should be idempotent.
	for i := 0; i < 3; i++ {
		if err := s.DeleteLease(key); err != nil {
			t.Fatalf("failed to delete lease: %v", err)
		}
	}

	_, ok, err := s.Lease(key)
	if err != nil {
		t.Fatalf("failed to get lease: %v", err)
	}
	if ok {
		t.Fatal("expected no lease but one was found")
	}
}

func testPurgeLeaseOK(t *testing.T, s wgipam.Store) {
	t.Helper()

	// Leases start every 100 seconds and last 10 seconds.
	const (
		start  = 100
		length = 10
	)

	var want []*wgipam.Lease
	for i := 0; i < 3; i++ {
		// Create leases which start at regular intervas.
		l := *okLease
		l.Start = time.Unix((int64(i)+1)*start, 0)
		l.Length = length * time.Second

		if i == 2 {
			// Track final lease for later comparison.
			want = []*wgipam.Lease{&l}
		}

		if err := s.SaveLease(uint64(i), &l); err != nil {
			t.Fatalf("failed to save lease: %v", err)
		}
	}

	// Purge only some of the leases by selecting a time that matches the
	// expiration time of the second lease.
	purge := time.Unix(2*start+length, 0)

	// Repeated purges with the same time should be idempotent.
	for i := 0; i < 3; i++ {
		if err := s.Purge(purge); err != nil {
			t.Fatalf("failed to purge leases: %v", err)
		}
	}

	// Expect only one lease to remain.
	got, err := s.Leases()
	if err != nil {
		t.Fatalf("failed to get lease: %v", err)
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("unexpected Leases (-want +got):\n%s", diff)
	}
}

func mustCIDR(s string) *net.IPNet {
	_, ipn, err := net.ParseCIDR(s)
	if err != nil {
		panicf("failed to parse CIDR: %v", err)
	}

	return ipn
}

func panicf(format string, a ...interface{}) {
	panic(fmt.Sprintf(format, a...))
}