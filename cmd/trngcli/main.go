// trngcli is a minimal example CLI demonstrating usage of the truerng package.
// It can read a specified number of bits once or at a fixed interval.
package main

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/Thiagojm/rng_go_cli/truerng"
)

func main() {
	bits := flag.Int("bits", 1024, "number of bits to read per batch")
	interval := flag.Duration("interval", 0, "interval between reads (e.g. 2s). 0 for one-shot")
	flag.Parse()

	present, err := truerng.Detect()
	if err != nil {
		log.Fatalf("detect error: %v", err)
	}
	if !present {
		log.Fatal("TrueRNG device not found")
	}

	if *interval == 0 {
		data, err := truerng.ReadBits(*bits)
		if err != nil {
			log.Fatalf("read error: %v", err)
		}
		fmt.Printf("read %d bits (%d bytes)\n", *bits, len(data))
		fmt.Printf("%s\n", hex.EncodeToString(data))
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	log.Printf("reading %d bits every %s. press Ctrl+C to stop...", *bits, interval.String())
	err = truerng.CollectBitsAtInterval(ctx, *bits, *interval, func(b []byte) {
		fmt.Printf("%s  %d bits  %s\n", time.Now().Format(time.RFC3339), *bits, hex.EncodeToString(b))
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("collect error: %v", err)
	}
}
