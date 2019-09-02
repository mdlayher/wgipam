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

// Command wgipamd implements an IP Address Management (IPAM) daemon for
// dynamic IP address assignment to WireGuard peers, using the wg-dynamic
// protocol.
//
// For more information about wg-dynamic, please see:
// https://git.zx2c4.com/wg-dynamic/about/.
//
// This project is not affiliated with the WireGuard or wg-dynamic projects.
package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"sync"

	"github.com/mdlayher/wgipam/internal/config"
	"github.com/mdlayher/wgipam/internal/wgipamd"
)

// cfgFile is the name of the default configuration file.
const cfgFile = "wgipamd.yaml"

func main() {
	var (
		cfgFlag  = flag.String("c", cfgFile, "path to configuration file")
		initFlag = flag.Bool("init", false,
			fmt.Sprintf("write out a default configuration file to %q and exit", cfgFile))
	)
	flag.Parse()

	ll := log.New(os.Stderr, "", log.LstdFlags)

	if *initFlag {
		if err := ioutil.WriteFile(cfgFile, []byte(config.Default), 0644); err != nil {
			ll.Fatalf("failed to write default configuration: %v", err)
		}

		return
	}

	f, err := os.Open(*cfgFlag)
	if err != nil {
		ll.Fatalf("failed to open configuration file: %v", err)
	}

	cfg, err := config.Parse(f)
	if err != nil {
		ll.Fatalf("failed to parse %q: %v", f.Name(), err)
	}
	_ = f.Close()

	ll.Printf("starting with configuration file %q", f.Name())

	// Use a context to handle cancelation on signal.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	defer wg.Wait()

	go func() {
		defer wg.Done()

		// Wait for signals (configurable per-platform) and then cancel the
		// context to indicate that the process should shut down.
		sigC := make(chan os.Signal, 1)
		signal.Notify(sigC, signals()...)

		s := <-sigC
		ll.Printf("received %s, shutting down", s)
		cancel()
	}()

	// Start the server's goroutines and run until context cancelation.
	if err := wgipamd.NewServer(*cfg, ll).Run(ctx); err != nil {
		ll.Fatalf("failed to run: %v", err)
	}
}
