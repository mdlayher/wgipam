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
	"fmt"
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
	t.Parallel()

	var (
		sub4 = wgipam.MustCIDR("192.0.2.0/32")
		sub6 = wgipam.MustCIDR("2001:db8::/128")
		ula6 = wgipam.MustCIDR("fdff:ffff::/128")

		zero4 = wgipam.MustCIDR("0.0.0.0/32")
		zero6 = wgipam.MustCIDR("::/128")

		reqDualStack = &wgdynamic.RequestIP{
			IPs: []*net.IPNet{zero4, zero6},
		}
		resDualStack = &wgdynamic.RequestIP{
			IPs:       []*net.IPNet{sub4, sub6, ula6},
			LeaseTime: 10 * time.Second,
		}
	)

	tests := []struct {
		name     string
		h        *wgipam.Handler
		req, res *wgdynamic.RequestIP
		err      *wgdynamic.Error
	}{
		// No addresses available in a given subnet.
		{
			name: "out of IPv4",
			h: func() *wgipam.Handler {
				h := mustHandler([]net.IPNet{*sub4, *sub6})

				if _, ok, err := h.IPs.Allocate(wgipam.IPv4); !ok || err != nil {
					t.Fatalf("failed to allocate last IPv4 address: %v, %v", ok, err)
				}

				return h
			}(),
			err: wgdynamic.ErrIPUnavailable,
		},
		{
			name: "out of IPv6",
			h: func() *wgipam.Handler {
				h := mustHandler([]net.IPNet{*sub4, *sub6})

				if _, ok, err := h.IPs.Allocate(wgipam.IPv6); !ok || err != nil {
					t.Fatalf("failed to allocate last IPv6 address: %v, %v", ok, err)
				}

				return h
			}(),
			err: wgdynamic.ErrIPUnavailable,
		},
		// Clients have no prior lease for these addresses and thus their
		// requests are refused.
		{
			name: "unavailable IPv4",
			h:    mustHandler([]net.IPNet{*sub4}),
			req: &wgdynamic.RequestIP{
				IPs: []*net.IPNet{sub4},
			},
			err: wgdynamic.ErrIPUnavailable,
		},
		{
			name: "unavailable IPv6",
			h:    mustHandler([]net.IPNet{*sub6}),
			req: &wgdynamic.RequestIP{
				IPs: []*net.IPNet{sub6},
			},
			err: wgdynamic.ErrIPUnavailable,
		},
		{
			name: "unavailable dual stack",
			h:    mustHandler([]net.IPNet{*sub4, *sub6}),
			req: &wgdynamic.RequestIP{
				IPs: []*net.IPNet{sub4, sub6},
			},
			err: wgdynamic.ErrIPUnavailable,
		},
		// Clients have a prior lease for one address but have requested a
		// different address.
		{
			name: "different IPv4",
			h: func() *wgipam.Handler {
				h := mustHandler([]net.IPNet{*sub4, *sub6})

				// Set a previous lease for this client with one address they
				// will not request.
				h.NewRequest = func(src net.Addr) {
					l := &wgipam.Lease{
						IPs: []*net.IPNet{
							sub6,
							wgipam.MustCIDR("192.0.2.255/32"),
						},
						Start:  wgipam.TimeNow(),
						Length: 10 * time.Second,
					}

					if err := h.Leases.SaveLease(wgipam.StrKey(src.String()), l); err != nil {
						t.Fatalf("failed to create initial lease: %v", err)
					}
				}

				return h
			}(),
			req: &wgdynamic.RequestIP{
				IPs: []*net.IPNet{sub6, sub4},
			},
			err: wgdynamic.ErrIPUnavailable,
		},
		{
			name: "different IPv6",
			h: func() *wgipam.Handler {
				h := mustHandler([]net.IPNet{*sub4, *sub6})

				// Set a previous lease for this client with one address they
				// will not request.
				h.NewRequest = func(src net.Addr) {
					l := &wgipam.Lease{
						IPs: []*net.IPNet{
							sub4,
							wgipam.MustCIDR("2001:db8::ffff/128"),
						},
						Start:  wgipam.TimeNow(),
						Length: 10 * time.Second,
					}

					if err := h.Leases.SaveLease(wgipam.StrKey(src.String()), l); err != nil {
						t.Fatalf("failed to create initial lease: %v", err)
					}
				}

				return h
			}(),
			req: &wgdynamic.RequestIP{
				IPs: []*net.IPNet{sub4, sub6},
			},
			err: wgdynamic.ErrIPUnavailable,
		},
		// Acceptable requests with and without an explicit IP address request.
		{
			name: "OK IPv4 no request",
			h:    mustHandler([]net.IPNet{*sub4}),
			res: &wgdynamic.RequestIP{
				IPs:       []*net.IPNet{sub4},
				LeaseTime: 10 * time.Second,
			},
		},
		{
			name: "OK IPv4 request",
			h:    mustHandler([]net.IPNet{*sub4}),
			req: &wgdynamic.RequestIP{
				IPs: []*net.IPNet{zero4},
			},
			res: &wgdynamic.RequestIP{
				IPs:       []*net.IPNet{sub4},
				LeaseTime: 10 * time.Second,
			},
		},
		{
			name: "OK IPv6 no request",
			h:    mustHandler([]net.IPNet{*sub6}),
			res: &wgdynamic.RequestIP{
				IPs:       []*net.IPNet{sub6},
				LeaseTime: 10 * time.Second,
			},
		},
		{
			name: "OK IPv6 request",
			h:    mustHandler([]net.IPNet{*sub6}),
			req: &wgdynamic.RequestIP{
				IPs: []*net.IPNet{zero6},
			},
			res: &wgdynamic.RequestIP{
				IPs:       []*net.IPNet{sub6},
				LeaseTime: 10 * time.Second,
			},
		},
		{
			name: "OK dual stack no request",
			h:    mustHandler([]net.IPNet{*sub4, *sub6, *ula6}),
			res:  resDualStack,
		},
		{
			name: "OK dual stack request",
			h:    mustHandler([]net.IPNet{*sub4, *sub6, *ula6}),
			req:  reqDualStack,
			res:  resDualStack,
		},
		{
			name: "OK renew dual stack no request",
			h: func() *wgipam.Handler {
				h := mustHandler([]net.IPNet{*sub4, *sub6, *ula6})

				// Set a previous lease for this client.
				h.NewRequest = func(src net.Addr) {
					l := &wgipam.Lease{
						IPs:    []*net.IPNet{sub4, sub6, ula6},
						Start:  wgipam.TimeNow(),
						Length: 10 * time.Second,
					}

					if err := h.Leases.SaveLease(wgipam.StrKey(src.String()), l); err != nil {
						t.Fatalf("failed to create initial lease: %v", err)
					}
				}

				return h
			}(),
			res: resDualStack,
		},
		{
			name: "OK renew dual stack request zero",
			h: func() *wgipam.Handler {
				h := mustHandler([]net.IPNet{*sub4, *sub6, *ula6})

				// Set a previous lease for this client.
				h.NewRequest = func(src net.Addr) {
					l := &wgipam.Lease{
						IPs:    []*net.IPNet{sub4, sub6, ula6},
						Start:  wgipam.TimeNow(),
						Length: 10 * time.Second,
					}

					if err := h.Leases.SaveLease(wgipam.StrKey(src.String()), l); err != nil {
						t.Fatalf("failed to create initial lease: %v", err)
					}
				}

				return h
			}(),
			req: reqDualStack,
			res: resDualStack,
		},
		{
			name: "OK dual stack expired lease",
			h: func() *wgipam.Handler {
				h := mustHandler([]net.IPNet{*sub4, *sub6, *ula6})

				// The lease is expired and should be ignored.
				h.NewRequest = func(src net.Addr) {
					l := &wgipam.Lease{
						// Use addresses that will not be allocated by our
						// configuration and verify they are removed.
						IPs: []*net.IPNet{
							wgipam.MustCIDR("192.0.2.255/32"),
							wgipam.MustCIDR("2001:db8::ffff/128"),
							wgipam.MustCIDR("fdff:ffff::ffff/128"),
						},
						Start:  time.Unix(1, 0),
						Length: 10 * time.Second,
					}

					if err := h.Leases.SaveLease(wgipam.StrKey(src.String()), l); err != nil {
						t.Fatalf("failed to create initial lease: %v", err)
					}
				}

				return h
			}(),
			res: resDualStack,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, done := testClient(t, tt.h)
			defer done()

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()

			res, err := c.RequestIP(ctx, tt.req)
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
			if time.Since(res.LeaseStart) > 10*time.Second {
				t.Fatalf("lease was created too long ago: %v", res.LeaseStart)
			}
			res.LeaseStart = time.Time{}

			if diff := cmp.Diff(tt.res, res); diff != "" {
				t.Fatalf("unexpected RequestIP (-want +got):\n%s", diff)
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
				IPs:    res.IPs,
				Length: res.LeaseTime,
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

func mustHandler(subs []net.IPNet) *wgipam.Handler {
	store := wgipam.MemoryStore()

	subnets := make([]wgipam.Subnet, 0, len(subs))
	for _, s := range subs {
		subnets = append(subnets, wgipam.Subnet{Subnet: s})
	}

	ipa, err := wgipam.DualStackIPAllocator(store, subnets)
	if err != nil {
		panicf("failed to create IPAllocators: %v", err)
	}

	return &wgipam.Handler{
		// Leases are always ephemeral in this test handler.
		Leases:        store,
		IPs:           ipa,
		LeaseDuration: 10 * time.Second,
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
		Dial: func(ctx context.Context) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "tcp", l.Addr().String())
		},
	}

	return c, func() {
		defer wg.Wait()

		if err := s.Close(); err != nil {
			t.Fatalf("failed to close server listener: %v", err)
		}
	}
}

func panicf(format string, a ...interface{}) {
	panic(fmt.Sprintf(format, a...))
}
