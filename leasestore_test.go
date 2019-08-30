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

package wgipam_test

import (
	"testing"

	"github.com/mdlayher/wgipam"
	"github.com/mdlayher/wgipam/internal/wgipamtest"
)

func TestLeaseStore(t *testing.T) {
	tests := []struct {
		name string
		mls  wgipamtest.MakeLeaseStore
	}{
		{
			name: "memory",
			mls: func(_ *testing.T) wgipam.LeaseStore {
				return wgipam.MemoryLeaseStore()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wgipamtest.TestLeaseStore(t, tt.mls)
		})
	}
}
