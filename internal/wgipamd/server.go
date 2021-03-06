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

package wgipamd

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/mdlayher/wgdynamic-go"
	"github.com/mdlayher/wgipam"
	"github.com/mdlayher/wgipam/internal/config"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/sync/errgroup"
)

// A Server coordinates the goroutines that handle various pieces of the
// wgipamd server.
type Server struct {
	cfg config.Config

	ll  *log.Logger
	reg *prometheus.Registry

	eg    *errgroup.Group
	ready chan struct{}

	// TestListener is an optional hook that replaces the wg-dynamic server
	// listener with the net.Listener created by this function. If nil, the
	// wg-dynamic default listener is used.
	TestListener func() (net.Listener, error)
}

// NewServer creates a Server with the input configuration and logger. If ll
// is nil, logs are discarded.
func NewServer(cfg config.Config, ll *log.Logger) *Server {
	if ll == nil {
		ll = log.New(ioutil.Discard, "", 0)
	}

	// Set up Prometheus instrumentation using the typical Go collectors.
	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(
		prometheus.NewGoCollector(),
		prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}),
	)

	return &Server{
		cfg: cfg,

		ll:  ll,
		reg: reg,

		ready: make(chan struct{}),
	}
}

// Ready indicates that the server is ready to begin serving requests.
func (s *Server) Ready() <-chan struct{} { return s.ready }

// Run runs the wgipamd server until the context is canceled.
func (s *Server) Run(ctx context.Context) error {
	// Attach the context to the errgroup so that goroutines are canceled when
	// one of them returns an error.
	eg, ctx := errgroup.WithContext(ctx)
	s.eg = eg

	// Serve on each specified interface.
	stores := make(map[string]wgipam.Store)
	for _, ifi := range s.cfg.Interfaces {
		// Configure storage based on the input configuration and then start
		// the wg-dynamic server for this interface.
		store, err := newStore(ifi.Name, s.cfg.Storage, s.ll)
		if err != nil {
			return fmt.Errorf("failed to configure storage: %v", err)
		}
		defer store.Close()

		// Track the store for each interface so it can be used in the debug
		// server HTTP API.
		stores[ifi.Name] = store

		if err := s.runServer(ctx, ifi, store); err != nil {
			return fmt.Errorf("failed to run wg-dynamic server: %v", err)
		}
	}

	// Configure the HTTP debug server, if applicable.
	if err := s.runDebug(ctx, stores); err != nil {
		return fmt.Errorf("failed to start debug HTTP server: %v", err)
	}

	// Indicate readiness to any waiting callers, and then wait for all
	// goroutines to be canceled and stopped successfully.
	close(s.ready)
	if err := s.eg.Wait(); err != nil {
		return fmt.Errorf("failed to serve: %v", err)
	}

	return nil
}

// runServer runs a wg-dynamic server for a single interface using goroutines,
// until ctx is canceled.
func (s *Server) runServer(ctx context.Context, ifi config.Interface, store wgipam.Store) error {
	var (
		l   net.Listener
		err error
	)

	// Use the default listener unless a test listener is configured.
	if s.TestListener == nil {
		l, err = wgdynamic.Listen(ifi.Name)
	} else {
		l, err = s.TestListener()
	}
	if err != nil {
		return fmt.Errorf("failed to listen on %q: %v", ifi.Name, err)
	}

	// Prepend the interface name to all logs for this server.
	logf := func(format string, v ...interface{}) {
		s.ll.Println(ifi.Name + ": " + fmt.Sprintf(format, v...))
	}

	logf("listening on %q, lease duration: %s, serving: %s",
		l.Addr(), ifi.LeaseDuration, subnetsString(ifi.Subnets))

	ips, err := wgipam.IPStoreMetrics(s.reg, ifi.Name, ifi.Subnets, store)
	if err != nil {
		return fmt.Errorf("failed to create IP store metrics: %v", err)
	}

	ipa, err := wgipam.DualStackIPAllocator(ips, ifi.Subnets)
	if err != nil {
		return fmt.Errorf("failed to create IP allocator: %v", err)
	}

	h := &wgipam.Handler{
		Leases:        store,
		IPs:           ipa,
		LeaseDuration: ifi.LeaseDuration,
		Log:           s.ll,
		Metrics:       wgipam.NewHandlerMetrics(s.reg, ifi.Name),
	}

	srv := &wgdynamic.Server{
		Log:       s.ll,
		RequestIP: h.RequestIP,
	}

	// Serve requests until the context is canceled.
	s.eg.Go(func() error {
		<-ctx.Done()
		return srv.Close()
	})

	s.eg.Go(func() error {
		return serve(srv.Serve(l))
	})

	// Purge expired leases immediately and also at regular intervals thereafter.
	s.eg.Go(func() error {
		tick := time.NewTicker(10 * time.Second)
		t := time.Now()
		for {
			// Ignore metrics; they are consumed internally.
			if _, err := ips.Purge(t); err != nil {
				logf("failed to purge expired data: %v", err)
			}

			select {
			case <-ctx.Done():
				return nil
			case t = <-tick.C:
			}
		}
	})

	return nil
}

// runDebug runs a debug HTTP server using goroutines, until ctx is canceled.
func (s *Server) runDebug(ctx context.Context, stores map[string]wgipam.Store) error {
	d := s.cfg.Debug
	if d.Address == "" {
		// Nothing to do, don't start the server.
		return nil
	}

	// Configure the HTTP debug server.
	l, err := net.Listen("tcp", d.Address)
	if err != nil {
		return fmt.Errorf("failed to start debug listener: %v", err)
	}

	s.ll.Printf("starting HTTP debug listener on %q: prometheus: %v, pprof: %v",
		d.Address, d.Prometheus, d.PProf)

	// Serve requests until the context is canceled.
	s.eg.Go(func() error {
		<-ctx.Done()
		return l.Close()
	})

	s.eg.Go(func() error {
		return serve(http.Serve(
			l, wgipam.NewHTTPHandler(d.Prometheus, d.PProf, s.reg, stores),
		))
	})

	return nil
}

// newStore configures a Store for the specified interface from
// storage configuration.
func newStore(ifi string, s config.Storage, ll *log.Logger) (wgipam.Store, error) {
	switch {
	case s.Memory:
		ll.Println("using ephemeral in-memory storage")
		return wgipam.MemoryStore(), nil
	case s.File != "":
		file := filepath.Join(s.File, fmt.Sprintf("wgipamd-%s.db", ifi))
		ll.Printf("using file %q storage", file)
		return wgipam.FileStore(file)
	default:
		return nil, fmt.Errorf("invalid storage configuration: %#v", s)
	}
}

// subnetsString turns subnets into a comma-separated string.
func subnetsString(subnets []wgipam.Subnet) string {
	var ss []string
	for _, s := range subnets {
		ss = append(ss, s.Subnet.String())
	}

	return strings.Join(ss, ", ")
}

// serve unpacks and handles certain network listener errors as appropriate.
func serve(err error) error {
	if err == nil {
		return nil
	}

	nerr, ok := err.(*net.OpError)
	if !ok {
		return err
	}

	// Unfortunately there isn't an easier way to check for this, but
	// we want to ignore errors related to the connection closing, since
	// s.Close is triggered on signal.
	if nerr.Err.Error() != "use of closed network connection" {
		return err
	}

	return nil
}
