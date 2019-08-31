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
	"io/ioutil"
	"os"
	"testing"

	"github.com/mdlayher/wgipam"
	"github.com/mdlayher/wgipam/wgipamtest"
)

func TestStore(t *testing.T) {
	// Set up a temporary directory for bolt database files which will also
	// be destroyed at the end of the test.
	dir, err := ioutil.TempDir("", "wgipamd-lease-store-test")
	if err != nil {
		t.Fatalf("failed to make temporary directory: %v", err)
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatalf("failed to clean up temporary directory: %v", err)
		}
	}()

	tests := []struct {
		name string
		mls  wgipamtest.MakeStore
	}{
		{
			name: "memory",
			mls: func(_ *testing.T) wgipam.Store {
				return wgipam.MemoryStore()
			},
		},
		{
			name: "file",
			mls: func(t *testing.T) wgipam.Store {
				// Create a random temporary file in the temporary directory
				// and use it as our file store.
				f, err := ioutil.TempFile(dir, "file.db")
				if err != nil {
					t.Fatalf("failed to create temporary file: %v", err)
				}
				_ = f.Close()

				ls, err := wgipam.FileStore(f.Name())
				if err != nil {
					t.Fatalf("failed to create file lease store: %v", err)
				}

				return ls
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wgipamtest.TestStore(t, tt.mls)
		})
	}
}