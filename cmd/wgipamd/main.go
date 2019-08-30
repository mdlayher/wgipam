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

// Command wgipamd implements an IP Address Management (IPAM) daemon for
// dynamic IP address assignment to WireGuard peers, using the wg-dynamic
// protocol.
//
// For more information about wg-dynamic, please see:
// https://git.zx2c4.com/wg-dynamic/about/.
//
// This project is not affiliated with the WireGuard or wg-dynamic projects.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/mdlayher/wgdynamic-go"
	"github.com/mdlayher/wgipam"
	"github.com/mdlayher/wgipam/internal/config"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/sync/errgroup"
)

// cfgFile is the name of the default configuration file.
const cfgFile = "wgipamd.yaml"

func main() {
	var (
		cfgFlag  = flag.String("c", cfgFile, "path to configuration file")
		initFlag = flag.Bool("init", false,
			fmt.Sprintf("write out a default configuration file to %q and exit", cfgFile))
	)
	flag.Parse()

	if *initFlag {
		if err := config.WriteDefault(cfgFile); err != nil {
			log.Fatalf("failed to write default configuration: %v", err)
		}

		return
	}

	f, err := os.Open(*cfgFlag)
	if err != nil {
		log.Fatalf("failed to open configuration file: %v", err)
	}

	cfg, err := config.Parse(f)
	if err != nil {
		log.Fatalf("failed to parse %q: %v", f.Name(), err)
	}
	_ = f.Close()

	log.Printf("starting with configuration file %q", f.Name())

	// Use a context to handle cancelation on signal.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var eg errgroup.Group

	eg.Go(func() error {
		// Wait for signals (configurable per-platform) and then cancel the
		// context to indicate that the process should shut down.
		sigC := make(chan os.Signal, 1)
		signal.Notify(sigC, signals()...)

		s := <-sigC
		log.Printf("received %s, shutting down", s)

		cancel()
		return nil
	})

	// Set up Prometheus instrumentation using the typical Go collectors.
	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(
		prometheus.NewGoCollector(),
		prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}),
	)

	// Configure the HTTP debug server, if applicable.
	if cfg.Debug.Address != "" {
		l, err := net.Listen("tcp", cfg.Debug.Address)
		if err != nil {
			log.Fatalf("failed to start debug listener: %v", err)
		}

		log.Printf("starting debug listener on %q: prometheus: %v, pprof: %v",
			cfg.Debug.Address, cfg.Debug.Prometheus, cfg.Debug.PProf)

		// Serve requests until the context is canceled.
		eg.Go(func() error {
			<-ctx.Done()
			return l.Close()
		})

		eg.Go(func() error {
			mux := http.NewServeMux()

			if cfg.Debug.Prometheus {
				mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
			}

			if cfg.Debug.PProf {
				mux.HandleFunc("/debug/pprof/", pprof.Index)
				mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
				mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
				mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
				mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
			}

			return serve(http.Serve(l, mux))
		})
	}

	// Serve on each specified interface.
	for _, ifi := range cfg.Interfaces {
		ll := log.New(os.Stderr, ifi.Name+": ", log.LstdFlags)

		// Configure lease storage based on the input configuration.
		store, err := newStore(ifi.Name, cfg.Storage, ll)
		if err != nil {
			ll.Fatalf("failed to configure lease storage: %v", err)
		}
		defer store.Close()

		l, err := wgdynamic.Listen(ifi.Name)
		if err != nil {
			ll.Fatalf("failed to listen on %q: %v", ifi.Name, err)
		}

		ll.Printf("listening on %q, serving: %s",
			l.Addr(), subnetsString(ifi.Subnets))

		ip4s, ip6s, err := wgipam.DualStackIPStore(ifi.Subnets)
		if err != nil {
			ll.Fatalf("failed to create IP stores: %v", err)
		}

		h := &wgipam.Handler{
			Log:    ll,
			IPv4:   ip4s,
			IPv6:   ip6s,
			Leases: store,
		}

		s := &wgdynamic.Server{
			Log:       ll,
			RequestIP: h.RequestIP,
		}

		// Serve requests until the context is canceled.
		eg.Go(func() error {
			<-ctx.Done()
			return s.Close()
		})

		eg.Go(func() error {
			return serve(s.Serve(l))
		})

		// Purge expired leases at regular intervals.
		eg.Go(func() error {
			tick := time.NewTicker(10 * time.Second)
			for {
				select {
				case <-ctx.Done():
					return nil
				case t := <-tick.C:
					if err := store.Purge(t); err != nil {
						ll.Printf("failed to purge expired data: %v", err)
					}
				}
			}
		})
	}

	if err := eg.Wait(); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

// newStore configures a Store for the specified interface from
// storage configuration.
func newStore(ifi string, s config.Storage, ll *log.Logger) (wgipam.Store, error) {
	switch {
	case s.Memory:
		ll.Println("using ephemeral in-memory storage")
		return wgipam.MemoryStore(), nil
	case s.File != "":
		file := fmt.Sprintf("%s-%s", ifi, s.File)
		ll.Printf("using file %q storage", file)
		return wgipam.FileStore(file)
	default:
		return nil, fmt.Errorf("invalid storage configuration: %#v", s)
	}
}

// subnetsString turns subnets into a comma-separated string.
func subnetsString(subnets []*net.IPNet) string {
	var ss []string
	for _, s := range subnets {
		ss = append(ss, s.String())
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
