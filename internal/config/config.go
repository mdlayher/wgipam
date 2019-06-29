package config

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net"

	"github.com/mikioh/ipaddr"
	"gopkg.in/yaml.v3"
)

// Default is the YAML representation of the default configuration.
const Default = `---
# Specify WireGuard interfaces to listen on for IP assignment requests.
interfaces:
- name: "wg0"
  # Specify one or more IPv4 and/or IPv6 subnets to allocate addresses from.
  subnets:
  - "192.0.2.0/24"
  - "2001:db8::/64"
`

// A file is the raw top-level configuration file representation.
type file struct {
	Interfaces []struct {
		Name    string   `yaml:"name"`
		Subnets []string `yaml:"subnets"`
	} `yaml:"interfaces"`
}

// Config specifies the configuration for wgipamd.
type Config struct {
	Interfaces []InterfaceConfig
}

// An InterfaceConfig provides configuration for an individual interface.
type InterfaceConfig struct {
	Name    string
	Subnets []*net.IPNet
}

// Parse parses a Config in YAML format from an io.Reader and verifies that
// the configuration is valid.
func Parse(r io.Reader) (*Config, error) {
	var f file
	if err := yaml.NewDecoder(r).Decode(&f); err != nil {
		return nil, err
	}

	// Don't bother to check for valid interface names; that is more easily
	// done when trying to create a listener. Instead, check for things
	// like subnet validity.
	c := &Config{
		Interfaces: make([]InterfaceConfig, 0, len(f.Interfaces)),
	}

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

		c.Interfaces = append(c.Interfaces, InterfaceConfig{
			Name:    ifi.Name,
			Subnets: subnets,
		})
	}

	return c, nil
}

// WriteDefault writes out the Default configuration to path.
func WriteDefault(path string) error {
	return ioutil.WriteFile(path, []byte(Default), 0644)
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
