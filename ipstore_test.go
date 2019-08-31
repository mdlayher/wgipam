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
	"fmt"
	"net"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/wgipam"
	"github.com/mikioh/ipaddr"
)

var (
	sub4 = mustCIDR("192.0.2.0/30")
	sub6 = mustCIDR("2001:db8::/126")
)

func TestDualStackIPAllocator(t *testing.T) {
	contains := func(t *testing.T, ips wgipam.IPAllocator, sub *net.IPNet) {
		t.Helper()

		// Allocate an address from the pool and verify it is contained within
		// the input subnet.
		ip, ok, err := ips.Allocate()
		if err != nil {
			t.Fatalf("failed to allocate from %q: %v", sub, err)
		}
		if !ok {
			t.Fatalf("did not allocate an IP address from %q", sub)
		}

		if !sub.Contains(ip.IP) {
			t.Fatalf("allocated IP %q is not within subnet %q", ip, sub)
		}
	}

	tests := []struct {
		name    string
		subnets []*net.IPNet
		ok      bool
		check   func(t *testing.T, ip4s, ip6s wgipam.IPAllocator)
	}{
		{
			name: "no subnets",
		},
		{
			name:    "OK IPv4 only",
			subnets: []*net.IPNet{sub4},
			ok:      true,
			check: func(t *testing.T, ip4s wgipam.IPAllocator, ip6s wgipam.IPAllocator) {
				if ip6s != nil {
					t.Fatal("allocated IPv6 store but no addresses specified")
				}

				contains(t, ip4s, sub4)
			},
		},
		{
			name:    "OK IPv6 only",
			subnets: []*net.IPNet{sub6},
			ok:      true,
			check: func(t *testing.T, ip4s wgipam.IPAllocator, ip6s wgipam.IPAllocator) {
				if ip4s != nil {
					t.Fatal("allocated IPv4 store but no addresses specified")
				}

				contains(t, ip6s, sub6)
			},
		},
		{
			name:    "OK dual stack",
			subnets: []*net.IPNet{sub4, sub6},
			ok:      true,
			check: func(t *testing.T, ip4s wgipam.IPAllocator, ip6s wgipam.IPAllocator) {
				contains(t, ip4s, sub4)
				contains(t, ip6s, sub6)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip4s, ip6s, err := wgipam.DualStackIPAllocator(wgipam.MemoryStore(), tt.subnets)
			if tt.ok && err != nil {
				t.Fatalf("failed to create IPAllocators: %v", err)
			}
			if !tt.ok && err == nil {
				t.Fatal("expected an error, but none occurred")
			}
			if err != nil {
				return
			}

			tt.check(t, ip4s, ip6s)
		})
	}
}

func TestIPAllocatorAllocate(t *testing.T) {
	tests := []struct {
		name    string
		subnets []*net.IPNet
		ok      bool
		check   func(t *testing.T, ipn *net.IPNet)
	}{
		{
			name: "no IPs",
		},
		{
			name:    "mixed IPv4",
			subnets: []*net.IPNet{sub4, sub6},
		},
		{
			name:    "mixed IPv6",
			subnets: []*net.IPNet{sub6, sub4},
		},
		{
			name:    "IPv4",
			subnets: []*net.IPNet{sub4},
			ok:      true,
			check: func(t *testing.T, ipn *net.IPNet) {
				if ipn.IP.To4() == nil {
					t.Fatalf("not an IPv4 address: %v", ipn.IP)
				}

				if diff := cmp.Diff(sub4.Mask, ipn.Mask); diff != "" {
					t.Fatalf("unexpected CIDR mask (-want +got):\n%s", diff)
				}
			},
		},
		{
			name:    "IPv6",
			subnets: []*net.IPNet{sub6},
			ok:      true,
			check: func(t *testing.T, ipn *net.IPNet) {
				if ipn.IP.To4() != nil {
					t.Fatalf("not an IPv6 address: %v", ipn.IP)
				}

				if diff := cmp.Diff(sub6.Mask, ipn.Mask); diff != "" {
					t.Fatalf("unexpected CIDR mask (-want +got):\n%s", diff)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ips, err := wgipam.SimpleIPAllocator(wgipam.MemoryStore(), tt.subnets)
			if tt.ok && err != nil {
				t.Fatalf("failed to create IPAllocator: %v", err)
			}
			if !tt.ok && err == nil {
				t.Fatal("expected an error, but none occurred")
			}
			if err != nil {
				return
			}

			var ipns []*net.IPNet
			for {
				ip, ok, err := ips.Allocate()
				if err != nil {
					t.Fatalf("failed to allocate IPs: %v", err)
				}
				if !ok {
					break
				}

				tt.check(t, ip)
				ipns = append(ipns, ip)
			}

			want := make([]ipaddr.Prefix, 0, len(tt.subnets))
			for _, s := range tt.subnets {
				want = append(want, *ipaddr.NewPrefix(s))
			}

			var got []ipaddr.Prefix
			if len(ipns) > 0 {
				got = ipaddr.Summarize(ipns[0].IP, ipns[len(ipns)-1].IP)
			}

			if diff := cmp.Diff(want, got); diff != "" {
				t.Fatalf("unexpected IP addresses (-want +got):\n%s", diff)
			}

			// Ensure all the addresses are freed as well.
			for _, ipn := range ipns {
				if err := ips.Free(ipn); err != nil {
					t.Fatalf("failed to free IP address: %v", err)
				}
			}
		})
	}
}

func TestIPAllocatorFree(t *testing.T) {
	tests := []struct {
		name    string
		subnets []*net.IPNet
		alloc   func(t *testing.T, ips wgipam.IPAllocator) *net.IPNet
		ok      bool
	}{
		{
			name:    "not allocated",
			subnets: []*net.IPNet{sub4},
			alloc: func(_ *testing.T, _ wgipam.IPAllocator) *net.IPNet {
				// Allocate a random address outside of sub4.
				return mustCIDR("192.0.2.1/32")
			},
			ok: true,
		},
		{
			name:    "allocated",
			subnets: []*net.IPNet{sub6},
			alloc: func(t *testing.T, ips wgipam.IPAllocator) *net.IPNet {
				// Allocate directly from sub6.
				ip, ok, err := ips.Allocate()
				if err != nil {
					t.Fatalf("failed to allocate IPs: %v", err)
				}
				if !ok {
					t.Fatal("out of IP addresses")
				}

				return ip
			},
			ok: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ips, err := wgipam.SimpleIPAllocator(wgipam.MemoryStore(), tt.subnets)
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
