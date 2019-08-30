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

// MakeLeaseStore is a function which produces a new wgipam.LeaseStore on each
// invocation. The LeaseStore should be completely empty when created.
type MakeLeaseStore func(t *testing.T) wgipam.LeaseStore

// TestLeaseStore tests a wgipam.LeaseStore type for compliance with the
// interface. The MakeLeaseStore function is invoked to retrieve a new and empty
// LeaseStore for each subtest.
func TestLeaseStore(t *testing.T, mls MakeLeaseStore) {
	t.Helper()

	tests := []struct {
		name string
		fn   func(t *testing.T, ls wgipam.LeaseStore)
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ls := mls(t)
			defer ls.Close()
			tt.fn(t, ls)
		})
	}
}

func testLeasesEmpty(t *testing.T, ls wgipam.LeaseStore) {
	t.Helper()

	leases, err := ls.Leases()
	if err != nil {
		t.Fatalf("failed to get leases: %v", err)
	}
	if diff := cmp.Diff(0, len(leases)); diff != "" {
		t.Fatalf("unexpected number of leases (-want +got):\n%s", diff)
	}
}

func testLeasesOK(t *testing.T, ls wgipam.LeaseStore) {
	t.Helper()

	// Save some synthetic leases to be fetched again later.
	for i := 0; i < 3; i++ {
		if err := ls.Save(uint64(i), okLease); err != nil {
			t.Fatalf("failed to save lease: %v", err)
		}
	}

	got, err := ls.Leases()
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

func testLeaseNotExist(t *testing.T, ls wgipam.LeaseStore) {
	t.Helper()

	l, ok, err := ls.Lease(1)
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

func testSaveLeaseOK(t *testing.T, ls wgipam.LeaseStore) {
	t.Helper()

	const key = 1
	if err := ls.Save(key, okLease); err != nil {
		t.Fatalf("failed to save lease: %v", err)
	}

	l, ok, err := ls.Lease(key)
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

func testDeleteLeaseOK(t *testing.T, ls wgipam.LeaseStore) {
	t.Helper()

	const key = 1
	if err := ls.Save(key, okLease); err != nil {
		t.Fatalf("failed to save lease: %v", err)
	}

	// Repeated deletions should be idempotent.
	for i := 0; i < 3; i++ {
		if err := ls.Delete(key); err != nil {
			t.Fatalf("failed to delete lease: %v", err)
		}
	}

	_, ok, err := ls.Lease(key)
	if err != nil {
		t.Fatalf("failed to get lease: %v", err)
	}
	if ok {
		t.Fatal("expected no lease but one was found")
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
