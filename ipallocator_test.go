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

package wgipam_test

import (
	"net"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/wgipam"
	"github.com/mikioh/ipaddr"
)

var (
	sub4 = wgipam.MustCIDR("192.0.2.0/30")
	sub6 = wgipam.MustCIDR("2001:db8::/126")
	ula6 = wgipam.MustCIDR("fd00:ffff::/126")
)

func TestDualStackIPAllocator(t *testing.T) {
	t.Parallel()

	contains := func(t *testing.T, ipa wgipam.IPAllocator, subs []net.IPNet) {
		t.Helper()

		// Allocate an address from the pool and verify it is contained within
		// the input subnet.
		ips, ok, err := ipa.Allocate(wgipam.DualStack)
		if err != nil {
			t.Fatalf("failed to allocate: %v", err)
		}
		if !ok {
			t.Fatalf("did not allocate IP addresses from %v", subs)
		}

		for _, ip := range ips {
			// Ensure each IP address resides within one of the input subnets.
			var found bool
			for _, s := range subs {
				if s.Contains(ip.IP) {
					found = true
					break
				}
			}

			if !found {
				t.Fatalf("allocated IPs %v not within subnets %v", ips, subs)
			}
		}
	}

	tests := []struct {
		name    string
		subnets []wgipam.Subnet
		ok      bool
		check   func(t *testing.T, ipa wgipam.IPAllocator)
	}{
		{
			name: "no subnets",
		},
		{
			name:    "OK IPv4 only",
			subnets: []wgipam.Subnet{{Subnet: *sub4}},
			ok:      true,
			check: func(t *testing.T, ipa wgipam.IPAllocator) {
				contains(t, ipa, []net.IPNet{*sub4})
			},
		},
		{
			name:    "OK IPv6 only",
			subnets: []wgipam.Subnet{{Subnet: *sub6}},
			ok:      true,
			check: func(t *testing.T, ipa wgipam.IPAllocator) {
				contains(t, ipa, []net.IPNet{*sub6})
			},
		},
		{
			name: "OK dual stack",
			subnets: []wgipam.Subnet{
				{Subnet: *sub4},
				{Subnet: *sub6},
				{Subnet: *ula6},
			},
			ok: true,
			check: func(t *testing.T, ipa wgipam.IPAllocator) {
				// Should get an address from each input subnet.
				contains(t, ipa, []net.IPNet{*sub4, *sub6, *ula6})
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ipa, err := wgipam.DualStackIPAllocator(wgipam.MemoryStore(), tt.subnets)
			if tt.ok && err != nil {
				t.Fatalf("failed to create IPAllocators: %v", err)
			}
			if !tt.ok && err == nil {
				t.Fatal("expected an error, but none occurred")
			}
			if err != nil {
				return
			}

			tt.check(t, ipa)
		})
	}
}

func TestSimpleIPAllocatorAllocateSubnet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		subnet net.IPNet
		check  func(t *testing.T, ipn *net.IPNet)
	}{

		{
			name:   "IPv4",
			subnet: *sub4,
			check: func(t *testing.T, ipn *net.IPNet) {
				if ipn.IP.To4() == nil {
					t.Fatalf("not an IPv4 address: %v", ipn.IP)
				}

				if diff := cmp.Diff(net.CIDRMask(32, 32), ipn.Mask); diff != "" {
					t.Fatalf("unexpected CIDR mask (-want +got):\n%s", diff)
				}
			},
		},
		{
			name:   "IPv6",
			subnet: *sub6,
			check: func(t *testing.T, ipn *net.IPNet) {
				if ipn.IP.To4() != nil {
					t.Fatalf("not an IPv6 address: %v", ipn.IP)
				}

				if diff := cmp.Diff(net.CIDRMask(128, 128), ipn.Mask); diff != "" {
					t.Fatalf("unexpected CIDR mask (-want +got):\n%s", diff)
				}
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ipa, err := wgipam.SimpleIPAllocator(wgipam.MemoryStore(), wgipam.Subnet{
				Subnet: tt.subnet,
			})
			if err != nil {
				t.Fatalf("failed to create IPAllocator: %v", err)
			}

			// All subnets verified to use the same family.
			f := wgipam.IPFamily(&tt.subnet)

			var ipns []*net.IPNet
			for {
				ips, ok, err := ipa.Allocate(f)
				if err != nil {
					t.Fatalf("failed to allocate IPs: %v", err)
				}
				if !ok {
					break
				}
				if len(ips) != 1 {
					t.Fatalf("expected 1 IP, but got: %d", len(ips))
				}

				tt.check(t, ips[0])
				ipns = append(ipns, ips...)
			}

			want := []ipaddr.Prefix{*ipaddr.NewPrefix(&tt.subnet)}

			var got []ipaddr.Prefix
			if len(ipns) > 0 {
				got = ipaddr.Summarize(ipns[0].IP, ipns[len(ipns)-1].IP)
			}

			if diff := cmp.Diff(want, got); diff != "" {
				t.Fatalf("unexpected IP addresses (-want +got):\n%s", diff)
			}

			// Ensure all the addresses are freed as well.
			for _, ipn := range ipns {
				if err := ipa.Free(ipn); err != nil {
					t.Fatalf("failed to free IP address: %v", err)
				}
			}
		})
	}
}

func TestSimpleIPAllocatorAllocateReserved(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		subnet wgipam.Subnet
		want   []string
	}{
		{
			name: "IPv4 no reserved",
			subnet: wgipam.Subnet{
				Subnet: *sub4,
			},
			want: []string{
				"192.0.2.0/32",
				"192.0.2.1/32",
				"192.0.2.2/32",
				"192.0.2.3/32",
			},
		},
		{
			name: "IPv4 start",
			subnet: wgipam.Subnet{
				Subnet: *sub4,
				Start:  net.IPv4(192, 0, 2, 2),
			},
			want: []string{
				"192.0.2.2/32",
				"192.0.2.3/32",
			},
		},
		{
			name: "IPv4 end",
			subnet: wgipam.Subnet{
				Subnet: *sub4,
				End:    net.IPv4(192, 0, 2, 1),
			},
			want: []string{
				"192.0.2.0/32",
				"192.0.2.1/32",
			},
		},
		{
			name: "IPv4 start/end",
			subnet: wgipam.Subnet{
				Subnet: *sub4,
				Start:  net.IPv4(192, 0, 2, 1),
				End:    net.IPv4(192, 0, 2, 2),
			},
			want: []string{
				"192.0.2.1/32",
				"192.0.2.2/32",
			},
		},
		{
			name: "IPv6 no reserved",
			subnet: wgipam.Subnet{
				Subnet: *sub6,
			},
			want: []string{
				"2001:db8::/128",
				"2001:db8::1/128",
				"2001:db8::2/128",
				"2001:db8::3/128",
			},
		},
		{
			name: "IPv6 start",
			subnet: wgipam.Subnet{
				Subnet: *sub6,
				Start:  net.ParseIP("2001:db8::2"),
			},
			want: []string{
				"2001:db8::2/128",
				"2001:db8::3/128",
			},
		},
		{
			name: "IPv6 end",
			subnet: wgipam.Subnet{
				Subnet: *sub6,
				End:    net.ParseIP("2001:db8::1"),
			},
			want: []string{
				"2001:db8::/128",
				"2001:db8::1/128",
			},
		},
		{
			name: "IPv6 start/end",
			subnet: wgipam.Subnet{
				Subnet: *sub6,
				Start:  net.ParseIP("2001:db8::1"),
				End:    net.ParseIP("2001:db8::2"),
			},
			want: []string{
				"2001:db8::1/128",
				"2001:db8::2/128",
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ipa, err := wgipam.SimpleIPAllocator(wgipam.MemoryStore(), tt.subnet)
			if err != nil {
				t.Fatalf("failed to create IPAllocator: %v", err)
			}

			// All subnets verified to use the same family.
			f := wgipam.IPFamily(&tt.subnet.Subnet)

			var got []string
			for {
				ips, ok, err := ipa.Allocate(f)
				if err != nil {
					t.Fatalf("failed to allocate IPs: %v", err)
				}
				if !ok {
					break
				}
				if len(ips) != 1 {
					t.Fatalf("expected 1 IP, but got: %d", len(ips))
				}

				got = append(got, ips[0].String())
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("unexpected IP addresses (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSimpleIPAllocatorFree(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		subnet net.IPNet
		alloc  func(t *testing.T, ips wgipam.IPAllocator) *net.IPNet
		ok     bool
	}{
		{
			name:   "not allocated",
			subnet: *sub4,
			alloc: func(_ *testing.T, _ wgipam.IPAllocator) *net.IPNet {
				// Allocate a random address outside of sub4.
				return wgipam.MustCIDR("192.0.2.1/32")
			},
			ok: true,
		},
		{
			name:   "allocated",
			subnet: *sub6,
			alloc: func(t *testing.T, ipa wgipam.IPAllocator) *net.IPNet {
				// Allocate directly from sub6.
				ips, ok, err := ipa.Allocate(wgipam.IPv6)
				if err != nil {
					t.Fatalf("failed to allocate IPs: %v", err)
				}
				if !ok {
					t.Fatal("out of IP addresses")
				}

				return ips[0]
			},
			ok: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ips, err := wgipam.SimpleIPAllocator(wgipam.MemoryStore(), wgipam.Subnet{
				Subnet: tt.subnet,
			})
			if err != nil {
				t.Fatalf("failed to create IPAllocator: %v", err)
			}

			err = ips.Free(tt.alloc(t, ips))
			if tt.ok && err != nil {
				t.Fatalf("failed to allocate and free IP address: %v", err)
			}
			if !tt.ok && err == nil {
				t.Fatal("expected an error, but none occurred")
			}
		})
	}
}

func TestSimpleIPAllocatorAllocateLoop(t *testing.T) {
	t.Parallel()

	ipa, err := wgipam.SimpleIPAllocator(wgipam.MemoryStore(), wgipam.Subnet{
		Subnet: *wgipam.MustCIDR("192.0.2.0/30"),
	})
	if err != nil {
		t.Fatalf("failed to create IPAllocator: %v", err)
	}

	// Loop through the /30 and keep allocating and freeing addresses. The
	// internal cursor should continue looping and handing out addresses which
	// are free at the beginning of the subnet.
	for i := 0; i < 16; i++ {
		ips, ok, err := ipa.Allocate(wgipam.IPv4)
		if err != nil {
			t.Fatalf("failed to allocate IPs: %v", err)
		}
		if !ok {
			t.Fatal("ran out of IPs")
		}
		if len(ips) != 1 {
			t.Fatalf("expected 1 IP, but got: %d", len(ips))
		}

		if diff := cmp.Diff(i%4, int(ips[0].IP.To4()[3])); diff != "" {
			t.Fatalf("unexpected final IP address octet (-want +got):\n%s", diff)
		}

		if err := ipa.Free(ips[0]); err != nil {
			t.Fatalf("failed to free IP address: %v", err)
		}
	}
}
