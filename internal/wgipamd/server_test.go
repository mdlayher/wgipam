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

package wgipamd_test

import (
	"context"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/promtest"
	"github.com/mdlayher/wgdynamic-go"
	"github.com/mdlayher/wgipam/internal/config"
	"github.com/mdlayher/wgipam/internal/wgipamd"
	"golang.org/x/sync/errgroup"
)

func TestServerRun(t *testing.T) {
	t.Parallel()

	host := mustCIDR("2001:db8::10/128")

	tests := []struct {
		name string
		cfg  config.Config

		// fn is a test case. cancel must be invoked by fn. srv and debug
		// specify the address strings for the wg-dynamic and HTTP debug
		// servers, respectively.
		fn func(t *testing.T, cancel func(), srv, debug string)
	}{
		{
			name: "no configuration",
		},
		{
			name: "debug no prometheus or pprof",
			cfg: config.Config{
				Debug: config.Debug{
					Address: randAddr(t),
				},
			},
			fn: func(t *testing.T, cancel func(), _, debug string) {
				defer cancel()

				// Debug listener should start, but Prometheus and pprof
				// endpoints should not.
				if !probeTCP(t, debug) {
					t.Fatal("debug listener did not start")
				}

				prom := httpGet(t, debug+"/metrics")
				if diff := cmp.Diff(http.StatusNotFound, prom.StatusCode); diff != "" {
					t.Fatalf("unexpected Prometheus HTTP status (-want +got):\n%s", diff)
				}

				pprof := httpGet(t, debug+"/debug/pprof/")
				if diff := cmp.Diff(http.StatusNotFound, pprof.StatusCode); diff != "" {
					t.Fatalf("unexpected pprof HTTP status (-want +got):\n%s", diff)
				}
			},
		},
		{
			name: "debug prometheus and pprof",
			cfg: config.Config{
				Debug: config.Debug{
					Address:    randAddr(t),
					Prometheus: true,
					PProf:      true,
				},
			},
			fn: func(t *testing.T, cancel func(), _, debug string) {
				defer cancel()

				// Debug listener should start with both configured endpoints
				// available.
				if !probeTCP(t, debug) {
					t.Fatal("debug listener did not start")
				}

				// TODO(mdlayher): validate metrics.
				prom := httpGet(t, debug+"/metrics")
				if diff := cmp.Diff(http.StatusOK, prom.StatusCode); diff != "" {
					t.Fatalf("unexpected Prometheus HTTP status (-want +got):\n%s", diff)
				}

				pprof := httpGet(t, debug+"/debug/pprof/")
				if diff := cmp.Diff(http.StatusOK, pprof.StatusCode); diff != "" {
					t.Fatalf("unexpected pprof HTTP status (-want +got):\n%s", diff)
				}
			},
		},
		{
			name: "server OK",
			cfg: config.Config{
				Storage: config.Storage{
					Memory: true,
				},
				Interfaces: []config.Interface{{
					Name:    "eth0",
					Subnets: []*net.IPNet{host},
				}},
				Debug: config.Debug{
					Address:    randAddr(t),
					Prometheus: true,
				},
			},
			fn: func(t *testing.T, cancel func(), srv, debug string) {
				defer cancel()

				// Both servers should be up and running.
				if !probeTCP(t, debug) {
					t.Fatal("debug listener did not start")
				}
				if !probeTCP(t, srv) {
					t.Fatal("wg-dynamic server did not start")
				}

				// Check the bare minimum functionality to ensure the server
				// works and started successfully. This will also populate
				// some Prometheus metrics for validation.
				res := requestIP(t, srv)
				if diff := cmp.Diff(host, res.IPv6); diff != "" {
					t.Fatalf("unexpected leased IPv6 address (-want +got):\n%s", diff)
				}

				prom := httpGet(t, debug+"/metrics")
				defer prom.Body.Close()

				if diff := cmp.Diff(http.StatusOK, prom.StatusCode); diff != "" {
					t.Fatalf("unexpected Prometheus HTTP status (-want +got):\n%s", diff)
				}

				b, err := ioutil.ReadAll(prom.Body)
				if err != nil {
					t.Fatalf("failed to read Prometheus metrics: %v", err)
				}

				// Validate the necessary metrics.
				// TODO(mdlayher): check for a whitelist of known metrics.
				if !promtest.Lint(t, b) {
					t.Fatal("Prometheus metrics are not lint-clean")
				}
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			s := wgipamd.NewServer(tt.cfg, nil)

			var (
				srv   string
				debug = tt.cfg.Debug.Address
			)

			// If an interface is configured, install the TestListener hook
			// so we can probe the wg-dynamic server.
			if len(tt.cfg.Interfaces) > 0 {
				s.TestListener = func() (net.Listener, error) {
					// Explicitly bind to IPv6 localhost as the wgdynamic
					// client library makes some assumptions about use of IPv6,
					// as does the wg-dynamic protocol.
					l, err := net.Listen("tcp6", "[::1]:0")
					if err != nil {
						return nil, err
					}

					// Capture the address so we can probe it later.
					srv = l.Addr().String()
					return l, nil
				}
			}

			var eg errgroup.Group
			eg.Go(func() error {
				return s.Run(ctx)
			})

			// Ensure the server has time to fully set up before we run tests.
			<-s.Ready()

			if tt.fn == nil {
				// If no function specified, just cancel the server immediately.
				cancel()
			} else {
				tt.fn(t, cancel, srv, debug)
			}

			if err := eg.Wait(); err != nil {
				t.Fatalf("failed to run server: %v", err)
			}

			// All services should be stopped.
			if probeTCP(t, srv) {
				t.Fatal("wg-dynamic server still running")
			}
			if probeTCP(t, debug) {
				t.Fatal("debug server still running")
			}
		})
	}
}

func probeTCP(t *testing.T, addr string) bool {
	t.Helper()

	// As a convenience, if the address is empty, we know that the service
	// cannot be probed.
	if addr == "" {
		return false
	}

	c, err := net.Dial("tcp", addr)
	if err == nil {
		_ = c.Close()
		return true
	}

	// String comparison isn't great but using build tags for syscall errno
	// seems like overkill.
	nerr, ok := err.(*net.OpError)
	if !ok || !strings.Contains(nerr.Err.Error(), "connection refused") {
		t.Fatalf("failed to dial TCP: %v", err)
	}

	return false
}

func httpGet(t *testing.T, addr string) *http.Response {
	t.Helper()

	addr = "http://" + addr
	u, err := url.Parse(addr)
	if err != nil {
		t.Fatalf("failed to parse URL: %v", err)
	}

	c := &http.Client{Timeout: 1 * time.Second}
	res, err := c.Get(u.String())
	if err != nil {
		t.Fatalf("failed to HTTP GET: %v", err)
	}

	return res
}

func requestIP(t *testing.T, addr string) *wgdynamic.RequestIP {
	t.Helper()

	taddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		t.Fatalf("failed to resolve address: %v", err)
	}

	c := &wgdynamic.Client{
		Dial: func(ctx context.Context) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "tcp", taddr.String())
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	res, err := c.RequestIP(ctx, nil)
	if err != nil {
		t.Fatalf("failed to request IP: %v", err)
	}

	return res
}

func randAddr(t *testing.T) string {
	t.Helper()

	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	_ = l.Close()

	return l.Addr().String()
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
