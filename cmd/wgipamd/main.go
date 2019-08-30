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
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"strings"

	"github.com/mdlayher/wgdynamic-go"
	"github.com/mdlayher/wgipam"
	"github.com/mdlayher/wgipam/internal/config"
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

	// Serve on each specified interface and wait for each goroutine to exit.
	var eg errgroup.Group
	for _, ifi := range cfg.Interfaces {
		ll := log.New(os.Stderr, ifi.Name+": ", log.LstdFlags)

		// Configure lease storage based on the input configuration.
		leases, err := newLeaseStore(ifi.Name, cfg.Storage, ll)
		if err != nil {
			ll.Fatalf("failed to configure lease storage: %v", err)
		}
		defer leases.Close()

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
			Leases: leases,
		}

		s := &wgdynamic.Server{
			Log:       ll,
			RequestIP: h.RequestIP,
		}

		eg.Go(func() error {
			return s.Serve(l)
		})
	}

	if err := eg.Wait(); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

// newLeaseStore configures a LeaseStore for the specified interface from
// storage configuration.
func newLeaseStore(ifi string, s config.Storage, ll *log.Logger) (wgipam.LeaseStore, error) {
	switch {
	case s.Memory:
		ll.Println("using ephemeral in-memory storage for leases")
		return wgipam.MemoryLeaseStore(), nil
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
