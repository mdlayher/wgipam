package wgipamd_test

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/wgipam/internal/config"
	"github.com/mdlayher/wgipam/internal/wgipamd"
	"golang.org/x/sync/errgroup"
)

func TestServerRun(t *testing.T) {
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

				// Debug listener should start with all endpoints available.
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			s := wgipamd.NewServer(tt.cfg, nil)

			var eg errgroup.Group
			eg.Go(func() error {
				return s.Run(ctx)
			})

			// Ensure the server has time to fully set up before we run tests.
			<-s.Ready()

			// TODO(mdlayher): test wg-dynamic server.
			srv := ""
			debug := tt.cfg.Debug.Address

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
			if srv != "" && probeTCP(t, srv) {
				t.Fatal("wg-dynamic server still running")
			}
			if debug != "" && probeTCP(t, debug) {
				t.Fatal("debug server still running")
			}
		})
	}
}

func probeTCP(t *testing.T, addr string) bool {
	t.Helper()

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

func randAddr(t *testing.T) string {
	t.Helper()

	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	_ = l.Close()

	return l.Addr().String()
}
