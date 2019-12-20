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
	"math"
	"net"
	"time"

	"github.com/mikioh/ipaddr"
	"github.com/prometheus/client_golang/prometheus"
)

// HandlerMetrics contains metrics related to Handler operations.
type HandlerMetrics struct {
	RequestsTotal *prometheus.CounterVec
	ErrorsTotal   *prometheus.CounterVec
}

// NewHandlerMetrics produces a HandlerMetrics structure which registers its
// metrics with reg and adds an interface label ifi.
func NewHandlerMetrics(reg *prometheus.Registry, ifi string) *HandlerMetrics {
	const subsystem = "server"

	labels := prometheus.Labels{
		"interface": ifi,
	}

	hm := &HandlerMetrics{
		RequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "requests_total",
			Help:      "The total number of requests from clients using the wg-dynamic protocol.",
		}, []string{"interface", "operation"}).MustCurryWith(labels),

		ErrorsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "errors_total",
			Help:      "Any errors returned to clients using the wg-dynamic protocol.",
		}, []string{"interface", "operation", "type"}).MustCurryWith(labels),
	}

	reg.MustRegister(hm.RequestsTotal)
	reg.MustRegister(hm.ErrorsTotal)

	return hm
}

// TODO: accept and return IPStore in Go 1.14.

type metricsIPStore struct {
	// Embedded so that we don't have to implement all methods.
	Store

	IPsAllocated          *prometheus.GaugeVec
	SubnetAllocatableSize *prometheus.GaugeVec
	LastPurge             *prometheus.GaugeVec

	subnets []Subnet
}

// IPStoreMetrics produces a Store which gathers Prometheus metrics for IPStore
// operations.
func IPStoreMetrics(reg *prometheus.Registry, ifi string, subnets []Subnet, ips Store) Store {
	const subsystem = "store"

	mis := &metricsIPStore{
		Store: ips,

		IPsAllocated: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "ips_allocated",
			Help:      "The current number of IP addresses allocated from a subnet.",
		}, []string{"subnet"}),

		SubnetAllocatableSize: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "subnet_allocatable_size",
			Help:      "The number of allocatable IP addresses within a subnet, accounting for start/end ranges and reserved addresses.",
		}, []string{"subnet"}),

		LastPurge: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "last_purge",
			Help:      "The UNIX timestamp of when the last expired lease/IP address purge occurred.",
		}, []string{"interface"}).MustCurryWith(prometheus.Labels{"interface": ifi}),

		subnets: subnets,
	}

	reg.MustRegister(mis.IPsAllocated)
	reg.MustRegister(mis.SubnetAllocatableSize)
	reg.MustRegister(mis.LastPurge)

	for _, s := range subnets {
		mis.SubnetAllocatableSize.WithLabelValues(s.Subnet.String()).Set(allocatableSize(s))
	}

	return mis
}

// AllocateIP implements IPStore.
func (mis *metricsIPStore) AllocateIP(subnet, ip *net.IPNet) (bool, error) {
	ok, err := mis.Store.AllocateIP(subnet, ip)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}

	mis.IPsAllocated.WithLabelValues(subnet.String()).Inc()
	return true, nil
}

// FreeIP implements IPStore.
func (mis *metricsIPStore) FreeIP(subnet, ip *net.IPNet) error {
	if err := mis.Store.FreeIP(subnet, ip); err != nil {
		return err
	}

	mis.IPsAllocated.WithLabelValues(subnet.String()).Dec()
	return nil
}

// Purge implements Store.
func (mis *metricsIPStore) Purge(t time.Time) (*PurgeStats, error) {
	stats, err := mis.Store.Purge(t)
	if err != nil {
		return nil, err
	}

	// Key is subnet, value is number of freed addresses.
	for sub, v := range stats.FreedIPs {
		mis.IPsAllocated.WithLabelValues(sub).Sub(float64(v))
	}

	mis.LastPurge.WithLabelValues().SetToCurrentTime()

	return stats, nil
}

// allocatableSize calculates the number of allocatable addresses within a
// Subnet, taking into account start/end ranges and reserved IP addresses.
func allocatableSize(s Subnet) float64 {
	// Handle empty input.
	if len(s.Subnet.IP) == 0 {
		return 0
	}

	p := ipaddr.NewPrefix(&s.Subnet)
	var (
		size float64

		// By default, start on the first and last IPs of a subnet.
		start = s.Subnet.IP
		end   = p.Last()
	)

	if s.Start != nil {
		start = s.Start
	}
	if s.End != nil {
		end = s.End
	}

	var ps []ipaddr.Prefix
	if s.Start == nil && s.End == nil {
		// No start or end boundary.
		ps = append(ps, *ipaddr.NewPrefix(&s.Subnet))
	} else {
		// Some defined start and/or end boundary.
		ps = ipaddr.Summarize(start, end)
	}

	for _, p := range ps {
		if p.NumNodes().IsUint64() {
			// We can represent this number as a uint64 so add it directly.
			size += float64(p.NumNodes().Uint64())
		} else {
			// Most likely a large IPv6 subnet; this number is effectively
			// infinite.
			size = math.Inf(1)
		}

		// Account for the number of reserved IPs within this prefix.
		for _, r := range s.Reserved {
			if p.IPNet.Contains(r) {
				size--
			}
		}
	}

	return size
}
