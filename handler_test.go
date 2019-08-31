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
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/wgdynamic-go"
	"github.com/mdlayher/wgipam"
)

func TestHandlerRequestIP(t *testing.T) {
	var (
		sub4 = mustCIDR("192.0.2.0/32")
		sub6 = mustCIDR("2001:db8::/128")

		errOutOfIPs = &wgdynamic.Error{
			Number:  1,
			Message: "out of IP addresses",
		}

		ripDualStack = &wgdynamic.RequestIP{
			IPv4:      sub4,
			IPv6:      sub6,
			LeaseTime: 10 * time.Second,
		}
	)

	tests := []struct {
		name string
		h    *wgipam.Handler
		rip  *wgdynamic.RequestIP
		err  *wgdynamic.Error
	}{
		{
			name: "out of IPs",
			h:    &wgipam.Handler{},
			err:  errOutOfIPs,
		},
		{
			name: "out of IPv4",
			h: func() *wgipam.Handler {
				h := mustHandler([]*net.IPNet{sub4, sub6})

				if _, ok, err := h.IPv4.Allocate(); !ok || err != nil {
					t.Fatalf("failed to allocate last IPv4 address: %v, %v", ok, err)
				}

				return h
			}(),
			err: errOutOfIPs,
		},
		{
			name: "out of IPv6",
			h: func() *wgipam.Handler {
				h := mustHandler([]*net.IPNet{sub4, sub6})

				if _, ok, err := h.IPv6.Allocate(); !ok || err != nil {
					t.Fatalf("failed to allocate last IPv6 address: %v, %v", ok, err)
				}

				return h
			}(),
			err: errOutOfIPs,
		},
		{
			name: "OK IPv4",
			h:    mustHandler([]*net.IPNet{sub4}),
			rip: &wgdynamic.RequestIP{
				IPv4:      sub4,
				LeaseTime: 10 * time.Second,
			},
		},
		{
			name: "OK IPv6",
			h:    mustHandler([]*net.IPNet{sub6}),
			rip: &wgdynamic.RequestIP{
				IPv6:      sub6,
				LeaseTime: 10 * time.Second,
			},
		},
		{
			name: "OK dual stack",
			h:    mustHandler([]*net.IPNet{sub4, sub6}),
			rip:  ripDualStack,
		},
		{
			name: "OK dual stack no leases",
			h: func() *wgipam.Handler {
				h := mustHandler([]*net.IPNet{sub4, sub6})

				// Leases explicitly disabled.
				h.Leases = nil
				return h
			}(),
			rip: ripDualStack,
		},
		{
			name: "OK dual stack with lease",
			h: func() *wgipam.Handler {
				h := mustHandler([]*net.IPNet{sub4, sub6})

				// Leases in use and one will be populated immediately when the
				// request is received, simulating an existing lease before the
				// allocation logic can kick in.
				h.NewRequest = func(src net.Addr) {
					l := &wgipam.Lease{
						IPv4:   sub4,
						IPv6:   sub6,
						Start:  wgipam.TimeNow(),
						Length: 10 * time.Second,
					}

					if err := h.Leases.SaveLease(wgipam.StrKey(src.String()), l); err != nil {
						t.Fatalf("failed to create initial lease: %v", err)
					}
				}

				return h
			}(),
			rip: ripDualStack,
		},
		{
			name: "OK dual stack with expired lease",
			h: func() *wgipam.Handler {
				h := mustHandler([]*net.IPNet{sub4, sub6})

				// Leases in use and one will be populated immediately when the
				// request is received. However, the lease is expired and should
				// be ignored.
				h.NewRequest = func(src net.Addr) {
					l := &wgipam.Lease{
						// Use an address that will not be allocated by our
						// configuration and verify it is removed.
						IPv4:   mustCIDR("192.0.2.255/32"),
						Start:  time.Unix(1, 0),
						Length: 10 * time.Second,
					}

					if err := h.Leases.SaveLease(wgipam.StrKey(src.String()), l); err != nil {
						t.Fatalf("failed to create initial lease: %v", err)
					}
				}

				return h
			}(),
			rip: ripDualStack,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, done := testClient(t, tt.h)
			defer done()

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()

			rip, err := c.RequestIP(ctx, nil)
			if err != nil {
				if tt.err == nil {
					t.Fatalf("failed to request IP: %v", err)
				}

				// Assume we've hit a protocol error and verify it.
				if diff := cmp.Diff(tt.err, err.(*wgdynamic.Error)); diff != "" {
					t.Fatalf("unexpected protocol error (-want +got):\n%s", diff)
				}

				return
			}

			// Equality with time is tricky, so just make sure the lease was
			// created recently and then do an exact comparison after clearing
			// the start time.
			if time.Since(rip.LeaseStart) > 10*time.Second {
				t.Fatalf("lease was created too long ago: %v", rip.LeaseStart)
			}
			rip.LeaseStart = time.Time{}

			if diff := cmp.Diff(tt.rip, rip); diff != "" {
				t.Fatalf("unexpected RequestIP (-want +got):\n%s", diff)
			}

			// Ensure a lease was populated if leases are in use.
			if tt.h.Leases == nil {
				return
			}

			leases, err := tt.h.Leases.Leases()
			if err != nil {
				t.Fatalf("failed to get leases: %v", err)
			}

			// Synthesize an expected Lease out of the parameters returned by
			// the server. The Start fields is nil'd out for comparisons as we
			// ultimately care mostly about the addresses assigned and the
			// duration.
			want := []*wgipam.Lease{{
				IPv4:   rip.IPv4,
				IPv6:   rip.IPv6,
				Length: rip.LeaseTime,
			}}

			for i := range leases {
				leases[i].Start = time.Time{}
			}

			if diff := cmp.Diff(want, leases); diff != "" {
				t.Fatalf("unexpected Leases (-want +got):\n%s", diff)
			}
		})
	}
}

func mustHandler(subnets []*net.IPNet) *wgipam.Handler {
	store := wgipam.MemoryStore()

	ip4s, ip6s, err := wgipam.DualStackIPAllocator(store, subnets)
	if err != nil {
		panicf("failed to create IPAllocators: %v", err)
	}

	return &wgipam.Handler{
		IPv4: ip4s,
		IPv6: ip6s,
		// Leases are always ephemeral in this test handler.
		Leases: store,
	}
}

func testClient(t *testing.T, h *wgipam.Handler) (*wgdynamic.Client, func()) {
	t.Helper()

	s := &wgdynamic.Server{
		RequestIP: h.RequestIP,
	}

	l, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()

		if err := s.Serve(l); err != nil {
			if strings.Contains(err.Error(), "use of closed network connection") {
				return
			}

			panicf("failed to serve: %v", err)
		}
	}()

	c := &wgdynamic.Client{
		RemoteAddr: l.Addr().(*net.TCPAddr),
	}

	return c, func() {
		defer wg.Wait()

		if err := s.Close(); err != nil {
			t.Fatalf("failed to close server listener: %v", err)
		}
	}
}
