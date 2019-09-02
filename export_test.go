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

package wgipam

import (
	"net"
	"time"
)

// Functions in this file should only be exported for use in tests.

// TimeNow wraps timeNow.
func TimeNow() time.Time { return timeNow() }

// StrKey wraps strKey.
func StrKey(s string) uint64 { return strKey(s) }

// MustCIDR parses s as a *net.IPNet or panics.
func MustCIDR(s string) *net.IPNet {
	_, ipn, err := net.ParseCIDR(s)
	if err != nil {
		panicf("failed to parse CIDR: %v", err)
	}

	return ipn
}
