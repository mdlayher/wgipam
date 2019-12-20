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
	"io"
	"net/http"
	"net/http/pprof"
	"sort"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// A HTTPHandler provides the HTTP debug API handler for wgipamd.
type HTTPHandler struct {
	h      http.Handler
	stores map[string]Store
}

// NewHTTPHandler creates a HTTPHandler with the specified configuration.
func NewHTTPHandler(
	usePrometheus, usePProf bool,
	reg *prometheus.Registry,
	stores map[string]Store,
) *HTTPHandler {
	mux := http.NewServeMux()

	h := &HTTPHandler{
		h:      mux,
		stores: stores,
	}

	mux.HandleFunc("/api/v0/interfaces", h.interfaces)

	// Optionally enable Prometheus and pprof support.
	if usePrometheus {
		mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	}

	if usePProf {
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	}

	return h
}

func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Matching on "/" would produce an overly broad rule, so check manually
	// here and indicate that this is the wgipamd service.
	if r.URL.Path == "/" {
		_, _ = io.WriteString(w, "wgipamd\n")
		return
	}

	h.h.ServeHTTP(w, r)
}

// interfaces returns JSON metadata about wgipam interfaces.
func (h *HTTPHandler) interfaces(w http.ResponseWriter, r *http.Request) {
	ac := apiContainer{
		Interfaces: make([]jsonInterface, 0, len(h.stores)),
	}

	for ifi, s := range h.stores {
		leases, err := s.Leases()
		if err != nil {
			http.Error(
				w,
				// This is a debug API so it's probably okay to show Go errors.
				fmt.Sprintf("failed to list leases: %v", err),
				http.StatusInternalServerError,
			)
			return
		}

		// Sort leases by when they started.
		sort.Slice(leases, func(i, j int) bool {
			return leases[i].Start.Before(leases[j].Start)
		})

		subnets, err := s.Subnets()
		if err != nil {
			http.Error(
				w,
				// This is a debug API so it's probably okay to show Go errors.
				fmt.Sprintf("failed to list subnets: %v", err),
				http.StatusInternalServerError,
			)
			return
		}

		ssubs := make([]string, 0, len(subnets))
		for _, s := range subnets {
			ssubs = append(ssubs, s.String())
		}

		sort.Strings(ssubs)

		// Unpack leases into JSON-friendly format.
		jif := jsonInterface{
			Name:    ifi,
			Subnets: ssubs,
		}
		for _, l := range leases {
			ips := make([]string, 0, len(l.IPs))
			for _, ip := range l.IPs {
				ips = append(ips, ip.String())
			}

			jif.Leases = append(jif.Leases, jsonLease{
				IPs:    ips,
				Start:  int(l.Start.Unix()),
				Length: int(l.Length.Seconds()),
			})
		}

		ac.Interfaces = append(ac.Interfaces, jif)
	}

	sort.Slice(ac.Interfaces, func(i, j int) bool {
		return ac.Interfaces[i].Name < ac.Interfaces[j].Name
	})

	_ = json.NewEncoder(w).Encode(ac)
}

// An apiContainer is the top-level API response object.
type apiContainer struct {
	Interfaces []jsonInterface `json:"interfaces"`
}

type jsonInterface struct {
	Name    string      `json:"name"`
	Subnets []string    `json:"subnets"`
	Leases  []jsonLease `json:"leases"`
}

// A jsonLease is the JSON representation of a Lease.
type jsonLease struct {
	IPs    []string `json:"ips"`
	Start  int      `json:"start"`
	Length int      `json:"length"`
}
