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

package config

import (
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/mikioh/ipaddr"
	"gopkg.in/yaml.v3"
)

// Default is the YAML representation of the default configuration.
const Default = `---
# The storage backend for IP address allocations and leases.
#
# If none specified, ephemeral in-memory storage will be used.
storage:
  file: "wgipamd.db"
# Specify one or more WireGuard interfaces to listen on for IP
# assignment requests.
interfaces:
  - name: "wg0"
    # Specify one or more IPv4 and/or IPv6 subnets to allocate addresses from.
    subnets:
      - "192.0.2.0/24"
      - "2001:db8::/64"
# Enable or disable the debug HTTP server for facilities such as Prometheus
# metrics and pprof support.
#
# Warning: do not expose pprof on an untrusted network!
debug:
  address: "localhost:9475"
  prometheus: true
  pprof: false
`

// A file is the raw top-level configuration file representation.
type file struct {
	Storage    storage `yaml:"storage"`
	Interfaces []struct {
		Name    string   `yaml:"name"`
		Subnets []string `yaml:"subnets"`
	} `yaml:"interfaces"`
	Debug Debug `yaml:"debug"`
}

// Config specifies the configuration for wgipamd.
type Config struct {
	Storage    Storage
	Interfaces []Interface
	Debug      Debug
}

// Storage provides configuration for storage backends.
type Storage struct {
	Memory bool
	File   string
}

// storage is the raw YAML structure for storage configuration.
type storage struct {
	File string `yaml:"file"`
}

// An Interface provides configuration for an individual interface.
type Interface struct {
	Name    string
	Subnets []*net.IPNet
}

// Debug provides configuration for debugging and observability.
type Debug struct {
	Address    string `yaml:"address"`
	Prometheus bool   `yaml:"prometheus"`
	PProf      bool   `yaml:"pprof"`
}

// Parse parses a Config in YAML format from an io.Reader and verifies that
// the configuration is valid.
func Parse(r io.Reader) (*Config, error) {
	var f file
	if err := yaml.NewDecoder(r).Decode(&f); err != nil {
		return nil, err
	}

	// Must configure at least one interface.
	if len(f.Interfaces) == 0 {
		return nil, errors.New("no configured interfaces")
	}

	c := &Config{
		Interfaces: make([]Interface, 0, len(f.Interfaces)),
	}

	// Validate debug configuration if set.
	if f.Debug.Address != "" {
		if _, err := net.ResolveTCPAddr("tcp", f.Debug.Address); err != nil {
			return nil, fmt.Errorf("bad debug address: %v", err)
		}
		c.Debug = f.Debug
	}

	s, err := parseStorage(f.Storage)
	if err != nil {
		return nil, err
	}
	c.Storage = *s

	// Don't bother to check for valid interface names; that is more easily
	// done when trying to create a listener. Instead, check for things
	// like subnet validity.
	for _, ifi := range f.Interfaces {
		if ifi.Name == "" {
			return nil, errors.New("empty interface name")
		}

		// Narrow down the location of a configuration error.
		handle := func(err error) error {
			return fmt.Errorf("interface %q: %v", ifi.Name, err)
		}

		subnets := make([]*net.IPNet, 0, len(ifi.Subnets))
		for _, s := range ifi.Subnets {
			ipn, err := parseCIDR(s)
			if err != nil {
				return nil, handle(err)
			}

			subnets = append(subnets, ipn)
		}

		if err := checkSubnets(subnets); err != nil {
			return nil, handle(err)
		}

		c.Interfaces = append(c.Interfaces, Interface{
			Name:    ifi.Name,
			Subnets: subnets,
		})
	}

	return c, nil
}

// parseStorage parses a raw storage configuration into a Storage structure.
func parseStorage(s storage) (*Storage, error) {
	if s.File == "" {
		return &Storage{Memory: true}, nil
	}

	return &Storage{File: s.File}, nil
}

// parseCIDR parses s as a *net.IPNet and verifies it refers to a subnet.
func parseCIDR(s string) (*net.IPNet, error) {
	ip, ipn, err := net.ParseCIDR(s)
	if err != nil {
		return nil, err
	}
	if !ip.Equal(ipn.IP) {
		return nil, fmt.Errorf("must specify a subnet, not an individual IP address: %s", s)
	}

	return ipn, err
}

// checkSubnets verifies the validity of subnets.
func checkSubnets(subnets []*net.IPNet) error {
	if len(subnets) == 0 {
		return errors.New("no subnets configured")
	}

	// Check if the same subnet appears more than once.
	seen := make(map[string]struct{}, len(subnets))
	for _, s := range subnets {
		str := s.String()
		if _, ok := seen[str]; ok {
			return fmt.Errorf("duplicate subnet: %s", str)
		}

		seen[str] = struct{}{}
	}

	// Check if any of the configured subnets overlap.
	for _, s1 := range subnets {
		for _, s2 := range subnets {
			p1, p2 := ipaddr.NewPrefix(s1), ipaddr.NewPrefix(s2)
			if !p1.Equal(p2) && p1.Overlaps(p2) {
				return fmt.Errorf("subnets overlap: %s and %s", s1, s2)
			}
		}
	}

	return nil
}
