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
	"math"
	"net"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func Test_allocatableSize(t *testing.T) {
	var (
		sub4 = *mustCIDR("192.0.2.0/29")
		sub6 = *mustCIDR("2001:db8::/64")
	)

	tests := []struct {
		name string
		s    Subnet
		size float64
	}{
		{
			name: "empty",
		},
		{
			name: "IPv4 no reserved",
			s: Subnet{
				Subnet: sub4,
			},
			size: 8,
		},
		{
			name: "IPv4 start",
			s: Subnet{
				Subnet: sub4,
				Start:  net.IPv4(192, 0, 2, 2),
			},
			size: 6,
		},
		{
			name: "IPv4 end",
			s: Subnet{
				Subnet: sub4,
				End:    net.IPv4(192, 0, 2, 1),
			},
			size: 2,
		},
		{
			name: "IPv4 start/end",
			s: Subnet{
				Subnet: sub4,
				Start:  net.IPv4(192, 0, 2, 1),
				End:    net.IPv4(192, 0, 2, 2),
			},
			size: 2,
		},
		{
			name: "IPv4 reserved",
			s: Subnet{
				Subnet: sub4,
				Reserved: []net.IP{
					net.IPv4(192, 0, 2, 0),
					net.IPv4(192, 0, 2, 1),
					net.IPv4(192, 0, 2, 3),
					net.IPv4(192, 0, 2, 6),
				},
			},
			size: 4,
		},
		{
			name: "IPv4 all",
			s: Subnet{
				Subnet: sub4,
				Start:  net.IPv4(192, 0, 2, 1),
				End:    net.IPv4(192, 0, 2, 5),
				Reserved: []net.IP{
					// Some addresses not within the range and should not
					// impact the total.
					net.IPv4(192, 255, 2, 1),
					net.IPv4(192, 0, 2, 1),
					net.IPv4(192, 0, 2, 3),
				},
			},
			size: 3,
		},
		{
			name: "IPv6 no reserved",
			s: Subnet{
				Subnet: sub6,
			},
			size: math.Inf(1),
		},
		{
			name: "IPv6 start",
			s: Subnet{
				Subnet: sub6,
				Start:  net.ParseIP("2001:db8:0:0:ffff:ffff:ffff:0"),
			},
			size: 65536,
		},
		{
			name: "IPv6 end",
			s: Subnet{
				Subnet: sub6,
				End:    net.ParseIP("2001:db8::ffff"),
			},
			size: 65536,
		},
		{
			name: "IPv6 start/end",
			s: Subnet{
				Subnet: sub6,
				Start:  net.ParseIP("2001:db8::1111"),
				End:    net.ParseIP("2001:db8::ffff"),
			},
			size: 61167,
		},
		{
			name: "IPv6 reserved",
			s: Subnet{
				Subnet: sub6,
				Reserved: []net.IP{
					net.ParseIP("2001:db8::0"),
					net.ParseIP("2001:db8::1"),
				},
			},
			size: math.Inf(1),
		},
		{
			name: "IPv6 all",
			s: Subnet{
				Subnet: sub6,
				Start:  net.ParseIP("2001:db8::1111"),
				End:    net.ParseIP("2001:db8::ffff"),
				Reserved: []net.IP{
					// Some addresses not within the range and should not
					// impact the total.
					net.ParseIP("2001:db8::0"),
					net.ParseIP("2001:db8::1"),
					net.ParseIP("2001:db8::1111"),
					net.ParseIP("2001:db8::1112"),
				},
			},
			size: 61165,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.size, allocatableSize(tt.s)); diff != "" {
				t.Fatalf("unexpected allocatable subnet size (-want +got):\n%s", diff)
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
