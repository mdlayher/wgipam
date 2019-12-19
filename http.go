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
	"path"
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

	// TODO(mdlayher): chances are pretty good that this will evolve into
	// something more than a "debug" API; e.g. adding the ability to immediately
	// revoke a lease and etc. This is something worth considering when adding
	// functionality to the API. At this time there are no stability guarantees.

	// Create a route to dump leases for each interface.
	for k := range h.stores {
		mux.HandleFunc("/leases/"+k, h.leases)
	}

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

// leases returns a JSON listing of the leases for an interface.
func (h *HTTPHandler) leases(w http.ResponseWriter, r *http.Request) {
	// Get the interface name from the last element of the path.
	s, ok := h.stores[path.Base(r.URL.Path)]
	if !ok {
		// TODO(mdlayher): is this impossible?
		http.NotFound(w, r)
		return
	}

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

	// Unpack leases into JSON-friendly format.
	ac := apiContainer{
		Leases: make([]jsonLease, 0, len(leases)),
	}
	for _, l := range leases {
		ips := make([]string, 0, len(l.IPs))
		for _, ip := range l.IPs {
			ips = append(ips, ip.String())
		}

		ac.Leases = append(ac.Leases, jsonLease{
			IPs:    ips,
			Start:  int(l.Start.Unix()),
			Length: int(l.Length.Seconds()),
		})
	}

	_ = json.NewEncoder(w).Encode(ac)
}

// An apiContainer is the top-level API response object.
type apiContainer struct {
	Leases []jsonLease `json:"leases"`
}

// A jsonLease is the JSON representation of a Lease.
type jsonLease struct {
	IPs    []string `json:"ips"`
	Start  int      `json:"start"`
	Length int      `json:"length"`
}
