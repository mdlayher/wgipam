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

package config_test

import (
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/wgipam/internal/config"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name string
		s    string
		c    *config.Config
		ok   bool
	}{
		{
			name: "bad YAML",
			s:    "xxx",
		},
		{
			name: "bad interface",
			s: `
---
interfaces:
- name: ""
`,
		},
		{
			name: "bad CIDR",
			s: `
---
interfaces:
- name: "wg0"
  subnets:
  - foo
`,
		},
		{
			name: "bad individual IP",
			s: `
---
interfaces:
- name: "wg0"
  subnets:
  - 192.0.2.1/24
`,
		},
		{
			name: "bad no subnets",
			s: `
---
interfaces:
- name: "wg0"
  subnets:
`,
		},
		{
			name: "bad duplicate subnet",
			s: `
---
interfaces:
- name: "wg0"
  subnets:
  - "192.0.2.0/24"
  - "192.0.2.0/24"
`,
		},
		{
			name: "bad subnet overlap",
			s: `
---
interfaces:
- name: "wg0"
  subnets:
  - "192.0.2.0/24"
  - "192.0.2.0/25"
`,
		},
		{
			name: "OK default",
			s:    config.Default,
			c: &config.Config{
				Interfaces: []config.InterfaceConfig{{
					Name: "wg0",
					Subnets: []*net.IPNet{
						mustCIDR("192.0.2.0/24"),
						mustCIDR("2001:db8::/64"),
					},
				}},
			},
			ok: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := config.Parse(strings.NewReader(tt.s))
			if tt.ok && err != nil {
				t.Fatalf("failed to parse config: %v", err)
			}
			if !tt.ok && err == nil {
				t.Fatal("expected an error, but none occurred")
			}
			if err != nil {
				return
			}

			if diff := cmp.Diff(tt.c, c); diff != "" {
				t.Fatalf("unexpected Config (-want +got):\n%s", diff)
			}
		})
	}
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
