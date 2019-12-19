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
	"encoding/json"
	"fmt"
	"net"
	"time"
)

// A Lease is a record of allocated IP addresses assigned to a client.
type Lease struct {
	IPs    []*net.IPNet
	Start  time.Time
	Length time.Duration
}

// String returns a string suitable for logging.
func (l *Lease) String() string {
	return fmt.Sprintf("IPs: %v, start: %s, end: %s",
		l.IPs,
		// time.Stamp seems to be reasonably readable.
		l.Start.Format(time.Stamp),
		l.Start.Add(l.Length).Format(time.Stamp),
	)
}

// Expired determines if the Lease is expired as of time t.
func (l *Lease) Expired(t time.Time) bool {
	exp := l.Start.Add(l.Length)
	return exp.Before(t) || exp.Equal(t)
}

// TODO(mdlayher): JSON is quick and easy but it's probably best to build out
// a binary format.

// marshal marshals a Lease to binary form.
func (l *Lease) marshal() ([]byte, error) {
	return json.Marshal(l)
}

// unmarshal unmarshals a Lease from binary form.
func (l *Lease) unmarshal(b []byte) error {
	return json.Unmarshal(b, l)
}
