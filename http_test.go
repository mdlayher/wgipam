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
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/prometheus/client_golang/prometheus"
)

func TestHTTPHandler(t *testing.T) {
	t.Parallel()

	store := MemoryStore()

	if err := store.SaveSubnet(MustCIDR("192.0.2.0/30")); err != nil {
		t.Fatalf("failed to store IPv4 subnet: %v", err)
	}
	if err := store.SaveSubnet(MustCIDR("2001:db8::/126")); err != nil {
		t.Fatalf("failed to store IPv6 subnet: %v", err)
	}

	l := &Lease{
		IPs:    []*net.IPNet{MustCIDR("192.0.2.0/32"), MustCIDR("2001:db8::/128")},
		Start:  time.Unix(1, 0),
		Length: 10 * time.Second,
	}

	if err := store.SaveLease(0, l); err != nil {
		t.Fatalf("failed to store lease: %v", err)
	}

	tests := []struct {
		name, path string
		ok         bool
		ac         apiContainer
	}{
		// We have some tests elsewhere for more intricate tests such as
		// Prometheus metrics usage, to these tests will mostly focus on
		// the JSON API aspects of the HTTP debug handler.
		{
			name: "not found",
			path: "/notfound",
		},
		{
			name: "interfaces OK",
			path: "/api/v0/interfaces",
			ok:   true,
			ac: apiContainer{
				Interfaces: []jsonInterface{{
					Name:    "wg0",
					Subnets: []string{"192.0.2.0/30", "2001:db8::/126"},
					Leases: []jsonLease{{
						IPs:    []string{l.IPs[0].String(), l.IPs[1].String()},
						Start:  int(l.Start.Unix()),
						Length: int(l.Length.Seconds()),
					}},
				}},
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(NewHTTPHandler(
				true, true, prometheus.NewRegistry(), map[string]Store{
					"wg0": store,
				},
			))
			defer srv.Close()

			u, err := url.Parse(srv.URL)
			if err != nil {
				t.Fatalf("failed to parse URL: %v", err)
			}
			u.Path = tt.path

			c := &http.Client{Timeout: 1 * time.Second}
			res, err := c.Get(u.String())
			if err != nil {
				t.Fatalf("failed to HTTP GET: %v", err)
			}
			defer res.Body.Close()

			wantCode := http.StatusNotFound
			if tt.ok {
				wantCode = http.StatusOK
			}

			if diff := cmp.Diff(wantCode, res.StatusCode); diff != "" {
				t.Fatalf("unexpected HTTP status (-want +got):\n%s", diff)
			}

			if !tt.ok {
				return
			}

			var ac apiContainer
			if err := json.NewDecoder(res.Body).Decode(&ac); err != nil {
				t.Fatalf("failed to decode API output: %v", err)
			}

			if diff := cmp.Diff(tt.ac, ac); diff != "" {
				t.Fatalf("unexpected API output (-want +got):\n%s", diff)
			}
		})
	}
}
