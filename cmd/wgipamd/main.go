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
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/mdlayher/wgipam/internal/config"
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

	if *initFlag {
		if err := config.WriteDefault(cfgFile); err != nil {
			log.Fatalf("failed to write default configuration: %v", err)
		}

		return
	}

	f, err := os.Open(*cfgFlag)
	if err != nil {
		log.Fatalf("failed to open configuration file: %v", err)
	}

	cfg, err := config.Parse(f)
	if err != nil {
		log.Fatalf("failed to parse %q: %v", f.Name(), err)
	}
	_ = f.Close()

	log.Printf("starting with configuration file %q", f.Name())

	// WIP.
	_ = cfg
}
