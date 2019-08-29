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
			rip: &wgdynamic.RequestIP{
				IPv4:      sub4,
				IPv6:      sub6,
				LeaseTime: 10 * time.Second,
			},
			err: errOutOfIPs,
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
			rip: &wgdynamic.RequestIP{
				IPv4:      sub4,
				IPv6:      sub6,
				LeaseTime: 10 * time.Second,
			},
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
			rip: &wgdynamic.RequestIP{
				IPv4:      sub4,
				IPv6:      sub6,
				LeaseTime: 10 * time.Second,
			},
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
			rip: &wgdynamic.RequestIP{
				IPv4:      sub4,
				IPv6:      sub6,
				LeaseTime: 10 * time.Second,
			},
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
		})
	}
}

func mustHandler(subnets []*net.IPNet) *wgipam.Handler {
	h, err := wgipam.NewHandler(subnets)
	if err != nil {
		panicf("failed to create handler: %v", err)
	}

	return h
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
