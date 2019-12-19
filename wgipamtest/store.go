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

// Populated fixtures for use in tests.
var (
	okLease = &wgipam.Lease{
		IPs:    []*net.IPNet{mustCIDR("192.0.2.0/32"), mustCIDR("2001:db8::/128")},
		Start:  time.Unix(1, 0),
		Length: 10 * time.Second,
	}

	okSubnet4 = mustCIDR("192.0.2.0/24")
	okIP4     = mustCIDR("192.0.2.1/32")
	okSubnet6 = mustCIDR("2001:db8::/64")
	okIP6     = mustCIDR("2001:db8::1/128")
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
			name: "purge OK",
			fn:   testPurgeOK,
		},
		{
			name: "subnets empty",
			fn:   testSubnetsEmpty,
		},
		{
			name: "subnets OK",
			fn:   testSubnetsOK,
		},
		{
			name: "allocated IPs no subnet",
			fn:   testAllocatedIPsNoSubnet,
		},
		{
			name: "allocate IP mismatched subnet",
			fn:   testAllocateIPMismatchedSubnet,
		},
		{
			name: "allocate IP no subnet",
			fn:   testAllocateIPNoSubnet,
		},
		{
			name: "allocate IP already allocated",
			fn:   testAllocateIPAlreadyAllocated,
		},
		{
			name: "free IP mismatched subnet",
			fn:   testFreeIPMismatchedSubnet,
		},
		{
			name: "free IP no subnet",
			fn:   testFreeIPNoSubnet,
		},
		{
			name: "free IP OK",
			fn:   testFreeIPOK,
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

func testPurgeOK(t *testing.T, s wgipam.Store) {
	t.Helper()

	if err := s.SaveSubnet(okSubnet4); err != nil {
		t.Fatalf("failed to save subnet: %v", err)
	}

	ipa, err := wgipam.DualStackIPAllocator(s, []net.IPNet{
		*okSubnet4, *okSubnet6,
	})
	if err != nil {
		t.Fatalf("failed to create IP allocator: %v", err)
	}

	// Leases start every 100 seconds and last 10 seconds.
	const (
		start  = 100
		length = 10
	)

	var want *wgipam.Lease
	for i := 0; i < 3; i++ {
		ips, ok, err := ipa.Allocate(wgipam.DualStack)
		if err != nil {
			t.Fatalf("failed to allocate IPs: %v", err)
		}
		if !ok {
			t.Fatal("ran out of IP addresses")
		}

		// Create leases which start at regular intervals.
		l := &wgipam.Lease{
			IPs:    ips,
			Start:  time.Unix((int64(i)+1)*start, 0),
			Length: length * time.Second,
		}

		if i == 2 {
			// Track final lease for later comparison.
			want = l
		}

		if err := s.SaveLease(uint64(i), l); err != nil {
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

	if diff := cmp.Diff([]*wgipam.Lease{want}, got); diff != "" {
		t.Fatalf("unexpected Leases (-want +got):\n%s", diff)
	}

	ip4s, err := s.AllocatedIPs(okSubnet4)
	if err != nil {
		t.Fatalf("failed to get allocated IPv4s: %v", err)
	}

	if diff := cmp.Diff([]*net.IPNet{want.IPs[0]}, ip4s); diff != "" {
		t.Fatalf("unexpected remaining IPv4 allocation (-want +got):\n%s", diff)
	}

	ip6s, err := s.AllocatedIPs(okSubnet6)
	if err != nil {
		t.Fatalf("failed to get allocated IPv6s: %v", err)
	}

	if diff := cmp.Diff([]*net.IPNet{want.IPs[1]}, ip6s); diff != "" {
		t.Fatalf("unexpected remaining IPv6 allocation (-want +got):\n%s", diff)
	}
}

func testSubnetsEmpty(t *testing.T, s wgipam.Store) {
	t.Helper()

	subnets, err := s.Subnets()
	if err != nil {
		t.Fatalf("failed to get subnets: %v", err)
	}
	if diff := cmp.Diff(0, len(subnets)); diff != "" {
		t.Fatalf("unexpected number of subnets (-want +got):\n%s", diff)
	}
}

func testSubnetsOK(t *testing.T, s wgipam.Store) {
	t.Helper()

	// Save some synthetic subnets to be fetched again later.
	want := []*net.IPNet{okSubnet4, okSubnet6}
	for _, sub := range want {
		if err := s.SaveSubnet(sub); err != nil {
			t.Fatalf("failed to save subnet: %v", err)
		}
	}

	got, err := s.Subnets()
	if err != nil {
		t.Fatalf("failed to get subnets: %v", err)
	}

	// No ordering guarantees are made, so sort both slices for comparison.
	sort.SliceStable(want, func(i, j int) bool {
		return want[i].String() < want[j].String()
	})
	sort.SliceStable(got, func(i, j int) bool {
		return got[i].String() < got[j].String()
	})

	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("unexpected Subnets (-want +got):\n%s", diff)
	}
}

func testAllocatedIPsNoSubnet(t *testing.T, s wgipam.Store) {
	t.Helper()

	if _, err := s.AllocatedIPs(okSubnet4); err == nil {
		t.Fatal("expected no such subnet error, but none occurred")
	}
}

func testAllocateIPMismatchedSubnet(t *testing.T, s wgipam.Store) {
	t.Helper()

	// An IPv6 address cannot possibly reside in an IPv4 subnet.
	if _, err := s.AllocateIP(okSubnet4, okIP6); err == nil {
		t.Fatal("expected mismatched subnet error, but none occurred")
	}
}

func testAllocateIPNoSubnet(t *testing.T, s wgipam.Store) {
	t.Helper()

	if _, err := s.AllocateIP(okSubnet4, okIP4); err == nil {
		t.Fatal("expected no such subnet error, but none occurred")
	}
}

func testAllocateIPAlreadyAllocated(t *testing.T, s wgipam.Store) {
	t.Helper()

	if err := s.SaveSubnet(okSubnet4); err != nil {
		t.Fatalf("failed to save subnet: %v", err)
	}

	// First call succeeds, second cannot.
	ok, err := s.AllocateIP(okSubnet4, okIP4)
	if err != nil || !ok {
		t.Fatalf("failed to allocate IP: ok: %v, err: %v", ok, err)
	}
	ok, err = s.AllocateIP(okSubnet4, okIP4)
	if err != nil || ok {
		t.Fatalf("expected IP already allocated: ok: %v, err: %v", ok, err)
	}
}

func testFreeIPMismatchedSubnet(t *testing.T, s wgipam.Store) {
	t.Helper()

	// An IPv6 address cannot possibly reside in an IPv4 subnet.
	if err := s.FreeIP(okSubnet4, okIP6); err == nil {
		t.Fatal("expected mismatched subnet error, but none occurred")
	}
}

func testFreeIPNoSubnet(t *testing.T, s wgipam.Store) {
	t.Helper()

	if err := s.FreeIP(okSubnet4, okIP4); err == nil {
		t.Fatal("expected no such subnet error, but none occurred")
	}
}

func testFreeIPOK(t *testing.T, s wgipam.Store) {
	t.Helper()

	// Allocate IP addresses in multiple subnets and ensure they can also
	// be freed idempotently.
	pairs := [][2]*net.IPNet{
		{okSubnet4, okIP4},
		{okSubnet6, okIP6},
	}

	for _, p := range pairs {
		if err := s.SaveSubnet(p[0]); err != nil {
			t.Fatalf("failed to save subnet: %v", err)
		}

		if ok, err := s.AllocateIP(p[0], p[1]); err != nil || !ok {
			t.Fatalf("failed to allocate IP: ok: %v, err: %v", ok, err)
		}

		// Repeated frees should be idempotent.
		for i := 0; i < 3; i++ {
			if err := s.FreeIP(p[0], p[1]); err != nil {
				t.Fatalf("failed to free IP: %v", err)
			}
		}
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
