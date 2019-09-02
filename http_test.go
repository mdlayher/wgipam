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
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/prometheus/client_golang/prometheus"
)

func TestHTTPHandler(t *testing.T) {
	l := &Lease{
		IPv4:   MustCIDR("192.0.2.0/32"),
		IPv6:   MustCIDR("2001:db8::/128"),
		Start:  time.Unix(1, 0),
		Length: 10 * time.Second,
	}

	store := MemoryStore()
	if err := store.SaveLease(0, l); err != nil {
		t.Fatalf("failed to store lease: %v", err)
	}

	tests := []struct {
		name, path string
		ok         bool
		leases     []jsonLease
	}{
		// We have some tests elswhere for more intricate tests such as
		// Prometheus metrics usage, to these tests will mostly focus on
		// the JSON API aspects of the HTTP debug handler.
		{
			name: "index",
			path: "/",
			ok:   true,
		},
		{
			name: "unknown interface",
			path: "/leases/wgnotexist0",
		},
		{
			name: "leases OK",
			path: "/leases/wg0",
			ok:   true,
			leases: []jsonLease{{
				IPv4:   l.IPv4.String(),
				IPv6:   l.IPv6.String(),
				Start:  int(l.Start.Unix()),
				Length: int(l.Length.Seconds()),
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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

			if !tt.ok || tt.leases == nil {
				return
			}

			var leases []jsonLease
			if err := json.NewDecoder(res.Body).Decode(&leases); err != nil {
				t.Fatalf("failed to decode leases: %v", err)
			}

			if diff := cmp.Diff(tt.leases, leases); diff != "" {
				t.Fatalf("unexpected leases(-want +got):\n%s", diff)
			}
		})
	}
}
