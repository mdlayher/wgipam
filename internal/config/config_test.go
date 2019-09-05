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
	t.Parallel()

	okInterfaces := []config.Interface{{
		Name:    "wg0",
		Subnets: []*net.IPNet{mustCIDR("192.0.2.0/24")},
	}}

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
			name: "bad no interfaces",
			s: `
---
interfaces:
`,
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
			name: "bad debug address",
			s: `
---
interfaces:
- name: "wg0"
debug:
  address: "xxx"
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
			name: "OK default storage no header",
			s: `
---
interfaces:
- name: "wg0"
  subnets:
  - "192.0.2.0/24"
`,
			c: &config.Config{
				Storage: config.Storage{
					Memory: true,
				},
				Interfaces: okInterfaces,
			},
			ok: true,
		},
		{
			name: "OK default storage with header",
			s: `
---
storage:
interfaces:
- name: "wg0"
  subnets:
  - "192.0.2.0/24"
`,
			c: &config.Config{
				Storage: config.Storage{
					Memory: true,
				},
				Interfaces: okInterfaces,
			},
			ok: true,
		},
		{
			name: "OK default storage with empty items",
			s: `
---
storage:
  file: ""
interfaces:
- name: "wg0"
  subnets:
  - "192.0.2.0/24"
`,
			c: &config.Config{
				Storage: config.Storage{
					Memory: true,
				},
				Interfaces: okInterfaces,
			},
			ok: true,
		},
		{
			name: "OK default",
			s:    config.Default,
			c: &config.Config{
				Storage: config.Storage{
					File: "wgipamd.db",
				},
				Interfaces: []config.Interface{{
					Name: "wg0",
					Subnets: []*net.IPNet{
						mustCIDR("192.0.2.0/24"),
						mustCIDR("2001:db8::/64"),
					},
				}},
				Debug: config.Debug{
					Address:    "localhost:9475",
					Prometheus: true,
					PProf:      false,
				},
			},
			ok: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

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
