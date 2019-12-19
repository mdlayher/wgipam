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
	"fmt"
	"log"
	"net"
	"time"

	"github.com/mdlayher/wgdynamic-go"
	"github.com/prometheus/client_golang/prometheus"
)

// A Handler handles IP allocation requests using the wg-dynamic protocol.
type Handler struct {
	// Leases specifies a Store for Lease storage. Leases must be non-nil or
	// the Handler will panic.
	Leases Store

	// IPv4 and IPv6 specify IPStores for IPv4 and IPv6 addresses, respectively.
	// If either are nil, addresses will not be allocated for that family.
	IPv4, IPv6 IPAllocator

	// LeaseDuration specifies the amount of time leases may exist before they
	// expire and are purged.
	LeaseDuration time.Duration

	// Log specifies a logger for the Server. If nil, all logs are discarded.
	Log *log.Logger

	// Metrics specifies Prometheus metrics for the Handler. If nil, metrics
	// are not collected.
	Metrics *HandlerMetrics

	// TODO(mdlayher): perhaps generalize NewRequest like net/http.ConnState?

	// NewRequest specifies an optional hook which will be invoked when a new
	// request is received. The source address src of the remote client is
	// passed to NewRequest. If NewRequest is nil, it is a no-op.
	NewRequest func(src net.Addr)
}

// RequestIP implements the wg-dynamic request_ip command.
func (h *Handler) RequestIP(src net.Addr, req *wgdynamic.RequestIP) (*wgdynamic.RequestIP, error) {
	if h.NewRequest != nil {
		// Hook is active, inform the caller of this request.
		h.NewRequest(src)
	}

	res, err := h.requestIP(src, req)
	if err != nil {
		h.metrics(func() {
			h.Metrics.RequestsTotal.WithLabelValues("request_ip", "error").Inc()

			// Note the type of error that occurs, if a protocol error is
			// sent back to the client.
			typ := "unknown"
			if werr, ok := err.(*wgdynamic.Error); ok {
				typ = werr.Message
			}

			h.Metrics.ErrorsTotal.WithLabelValues("request_ip", typ).Inc()
		})
		return nil, err
	}

	h.metrics(func() {
		h.Metrics.RequestsTotal.WithLabelValues("request_ip", "ok").Inc()
	})
	return res, nil
}

func (h *Handler) requestIP(src net.Addr, req *wgdynamic.RequestIP) (*wgdynamic.RequestIP, error) {
	// Check for an existing lease.
	key := strKey(src.String())
	l, ok, err := h.Leases.Lease(key)
	if err != nil {
		return nil, err
	}
	if !ok {
		// No lease, create a new lease.
		return h.newLease(src, req)
	}

	if time.Since(l.Start.Add(l.Length)) < 0 {
		// Lease has not expired, renew it.
		return h.renewLease(src, l)
	}

	// Clean up data related to the existing lease and create a new one.
	h.logf(src, "deleting expired lease and freeing IP addresses: %s", l)

	// TODO(mdlayher): better error handling, Prometheus metrics, etc.
	// Should failure to delete due to an item not existing actually be an
	// error? It seems like it'll create more noise than necessary.
	if err := h.Leases.DeleteLease(key); err != nil {
		h.logf(src, "failed to delete lease %s: %v", l, err)
	}
	if err := h.free(l.IPs); err != nil {
		h.logf(src, "failed to free IP addresses %v: %v", l.IPs, err)
	}

	return h.newLease(src, req)
}

func (h *Handler) newLease(src net.Addr, req *wgdynamic.RequestIP) (*wgdynamic.RequestIP, error) {
	ips, err := h.allocate(src, req)
	if err != nil {
		// Clear any temporarily allocated IPs before returning the error to
		// the caller.
		if err := h.free(ips); err != nil {
			h.logf(src, "failed to free temporarily allocated IP addresses %v: %v", ips, err)
		}

		return nil, err
	}

	l := &Lease{
		IPs:    ips,
		Start:  timeNow(),
		Length: h.LeaseDuration,
	}

	h.logf(src, "creating new IP address lease: %s", l)

	if err := h.Leases.SaveLease(strKey(src.String()), l); err != nil {
		return nil, err
	}

	return &wgdynamic.RequestIP{
		IPs:        ips,
		LeaseStart: l.Start,
		LeaseTime:  l.Length,
	}, nil
}

// renewLease renews a Lease for clients who have an existing Lease.
func (h *Handler) renewLease(src net.Addr, l *Lease) (*wgdynamic.RequestIP, error) {
	// We have a current lease, honor it and update its expiration time.
	// TODO(mdlayher): lease expiration, parameterize expiration time.
	l.Start = timeNow()
	l.Length = h.LeaseDuration

	h.logf(src, "renewing IP address lease: %s", l)

	if err := h.Leases.SaveLease(strKey(src.String()), l); err != nil {
		return nil, err
	}

	return &wgdynamic.RequestIP{
		IPs:        l.IPs,
		LeaseStart: l.Start,
		LeaseTime:  l.Length,
	}, nil
}

// HandlerMetrics contains metrics related to Handler operations.
type HandlerMetrics struct {
	RequestsTotal *prometheus.CounterVec
	ErrorsTotal   *prometheus.CounterVec
}

// NewHandlerMetrics produces a HandlerMetrics structure which registers its
// metrics with reg and adds an interface label ifi.
func NewHandlerMetrics(reg *prometheus.Registry, ifi string) *HandlerMetrics {
	const (
		namespace = "wgipamd"
		subsystem = "server"
	)

	labels := prometheus.Labels{
		"interface": ifi,
	}

	hm := &HandlerMetrics{
		RequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "requests_total",
			Help:      "The total number of requests from clients using the wg-dynamic protocol.",
		}, []string{"interface", "operation", "status"}).MustCurryWith(labels),

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

// allocate allocates one or more IP addresses based on the client's request
// and the availability of addresses in the pools.
func (h *Handler) allocate(src net.Addr, _ *wgdynamic.RequestIP) ([]*net.IPNet, error) {
	// TODO: honor requests for specific IP.

	type poolPair struct {
		p IPAllocator
		s string
	}

	var ips []*net.IPNet
	for _, p := range []poolPair{
		{p: h.IPv4, s: "IPv4"},
		{p: h.IPv6, s: "IPv6"},
	} {
		// No pool, nothing to do.
		if p.p == nil {
			continue
		}

		ip, ok, err := p.p.Allocate()
		if err != nil {
			return ips, err
		}

		if !ok {
			h.logf(src, "out of %s addresses", p.s)
			return ips, &wgdynamic.Error{
				Number:  1,
				Message: "out of IP addresses",
			}
		}

		ips = append(ips, ip)
	}

	return ips, nil
}

// free frees an IP addresses in ips. If ips or ip are nil, it returns early.
func (h *Handler) free(ips []*net.IPNet) error {
	var pool IPAllocator
	for _, ip := range ips {
		if ip.IP.To16() != nil && ip.IP.To4() != nil {
			if h.IPv4 == nil {
				continue
			}
			pool = h.IPv4
		} else {
			if h.IPv6 == nil {
				continue
			}
			pool = h.IPv6
		}

		if err := pool.Free(ip); err != nil {
			return fmt.Errorf("failed to free %s: %v", ip, err)
		}
	}

	return nil
}

// logf logs a formatted message about src, if h.Log is configured.
func (h *Handler) logf(src net.Addr, format string, v ...interface{}) {
	if h.Log == nil {
		return
	}

	ta, ok := src.(*net.TCPAddr)
	if !ok {
		// Unknown address type, just print the whole thing.
		h.Log.Printf("%s: %s", src, fmt.Sprintf(format, v...))
		return
	}

	// Port and zone aren't necessary.
	h.Log.Printf("%s: %s", ta.IP, fmt.Sprintf(format, v...))
}

// metrics invokes fn if h.Metrics is configured.
func (h *Handler) metrics(fn func()) {
	if h.Metrics == nil {
		return
	}

	fn()
}

func panicf(format string, a ...interface{}) {
	panic(fmt.Sprintf(format, a...))
}
