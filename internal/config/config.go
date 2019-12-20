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
	"time"

	"github.com/BurntSushi/toml"
	"github.com/mdlayher/wgipam"
	"github.com/mikioh/ipaddr"
)

//go:generate embed file -var Default --source default.toml

// Default is the toml representation of the default configuration.
var Default = "# wgipamd vALPHA configuration file\n\n# The storage backend for IP address allocations and leases.\n#\n# If none specified, ephemeral in-memory storage will be used.\n[storage]\nfile = \"/var/lib/wgipamd\"\n\n# Specify one or more WireGuard interfaces to listen on for IP\n# assignment requests.\n[[interfaces]]\nname = \"wg0\"\n\n# Optional: the amount of time a lease will exist before it expires, specified\n# in Go time.ParseDuration format (https://golang.org/pkg/time/#ParseDuration).\n# If not set or empty, defaults to 1 hour.\nlease_duration = \"1h\"\n\n  # Specify one or more IPv4 and/or IPv6 subnets to allocate addresses from.\n  [[interfaces.subnets]]\n  subnet = \"192.0.2.0/24\"\n  # Optional: specify a range of addresses within the subnet which will be\n  # used for leases. For example, this can be used to skip over statically\n  # allocated peer addresses before or after this range.\n  #\n  # Both start and end are optional and either may be omitted to use the\n  # first and last addresses in a range.\n  #\n  # Addresses in this range can be individually excluded by adding them\n  # to the reserved list for this subnet.\n  start = \"192.0.2.10\"\n  end = \"192.0.2.255\"\n  # Optional: specify individual addresses within the subnet which are\n  # reserved and will not be used for leases. For example, this can be\n  # used to reserve certain addresses for static peer allocations.\n  reserved = [\n    \"192.0.2.255\"\n  ]\n\n  [[interfaces.subnets]]\n  subnet = \"2001:db8::/64\"\n  # Optional: see above.\n  reserved = [\n    \"2001:db8::\",\n    \"2001:db8::1\"\n  ]\n\n# Enable or disable the debug HTTP server for facilities such as Prometheus\n# metrics and pprof support.\n#\n# Warning: do not expose pprof on an untrusted network!\n[debug]\naddress = \"localhost:9475\"\nprometheus = true\npprof = false\n"

// A file is the raw top-level configuration file representation.
type file struct {
	Storage    storage `toml:"storage"`
	Interfaces []struct {
		Name          string   `toml:"name"`
		LeaseDuration string   `toml:"lease_duration"`
		Subnets       []subnet `toml:"subnets"`
	} `toml:"interfaces"`
	Debug Debug `toml:"debug"`
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

// storage is the raw TOML structure for storage configuration.
type storage struct {
	File string `toml:"file"`
}

// An Interface provides configuration for an individual interface.
type Interface struct {
	Name          string
	LeaseDuration time.Duration
	Subnets       []wgipam.Subnet
}

// A subnet is the raw TOML structure for subnet configuration.
type subnet struct {
	Subnet   string   `toml:"subnet"`
	Start    string   `toml:"start"`
	End      string   `toml:"end"`
	Reserved []string `toml:"reserved"`
}

// Debug provides configuration for debugging and observability.
type Debug struct {
	Address    string `toml:"address"`
	Prometheus bool   `toml:"prometheus"`
	PProf      bool   `toml:"pprof"`
}

// Parse parses a Config in toml format from an io.Reader and verifies that
// the configuration is valid.
func Parse(r io.Reader) (*Config, error) {
	var f file
	md, err := toml.DecodeReader(r, &f)
	if err != nil {
		return nil, err
	}
	if u := md.Undecoded(); len(u) > 0 {
		return nil, fmt.Errorf("unrecognized configuration keys: %s", u)
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

		// Default to 1 hour leases, but parse a different value if specified.
		dur := 1 * time.Hour
		if ifi.LeaseDuration != "" {
			d, err := time.ParseDuration(ifi.LeaseDuration)
			if err != nil {
				return nil, fmt.Errorf("invalid lease duration: %v", err)
			}

			dur = d
		}

		subnets := make([]wgipam.Subnet, 0, len(ifi.Subnets))
		for _, s := range ifi.Subnets {
			sub, err := parseSubnet(s)
			if err != nil {
				return nil, handle(err)
			}

			subnets = append(subnets, *sub)
		}

		if err := checkSubnets(subnets); err != nil {
			return nil, handle(err)
		}

		c.Interfaces = append(c.Interfaces, Interface{
			Name:          ifi.Name,
			LeaseDuration: dur,
			Subnets:       subnets,
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

// parseSubnet parses a raw subnet into a wgipam.Subnet structure.
func parseSubnet(s subnet) (*wgipam.Subnet, error) {
	ip, sub, err := net.ParseCIDR(s.Subnet)
	if err != nil {
		return nil, err
	}

	// Narrow down the location of a configuration error.
	errorf := func(format string, v ...interface{}) error {
		return fmt.Errorf("subnet %s: %s", s.Subnet, fmt.Sprintf(format, v...))
	}

	if !ip.Equal(sub.IP) {
		return nil, errorf("must specify a subnet, not an individual IP address")
	}

	// start and end are optional and are only set if not empty.
	var start, end net.IP

	if s.Start != "" {
		start = net.ParseIP(s.Start)
		if start == nil {
			return nil, errorf("invalid start range IP: %s", s.Start)
		}
	}

	if s.End != "" {
		end = net.ParseIP(s.End)
		if end == nil {
			return nil, errorf("invalid end range IP: %s", s.End)
		}
	}

	reserved := make([]net.IP, 0, len(s.Reserved))
	for _, r := range s.Reserved {
		res := net.ParseIP(r)
		if res == nil {
			return nil, errorf("invalid reserved IP: %s", r)
		}

		reserved = append(reserved, res)
	}

	// If empty, nil out for easier comparison in tests.
	if len(reserved) == 0 {
		reserved = nil
	}

	subnet := &wgipam.Subnet{
		Subnet:   *sub,
		Start:    start,
		End:      end,
		Reserved: reserved,
	}

	if err := subnet.Validate(); err != nil {
		// No need to further annotate; we just want the subnet info.
		return nil, errorf("%v", err)
	}

	return subnet, nil
}

// checkSubnets verifies the validity of Subnets, checking for properties such
// as subnet overlap.
func checkSubnets(subnets []wgipam.Subnet) error {
	if len(subnets) == 0 {
		return errors.New("no subnets configured")
	}

	// Check if the same subnet appears more than once.
	seen := make(map[string]struct{}, len(subnets))
	for _, s := range subnets {
		str := s.Subnet.String()
		if _, ok := seen[str]; ok {
			return fmt.Errorf("duplicate subnet: %s", str)
		}

		seen[str] = struct{}{}
	}

	// Check if any of the configured subnets overlap.
	for _, s1 := range subnets {
		for _, s2 := range subnets {
			p1, p2 := ipaddr.NewPrefix(&s1.Subnet), ipaddr.NewPrefix(&s2.Subnet)
			if !p1.Equal(p2) && p1.Overlaps(p2) {
				return fmt.Errorf("subnets overlap: %s and %s", &s1.Subnet, &s2.Subnet)
			}
		}
	}

	return nil
}
