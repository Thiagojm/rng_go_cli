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

	"github.com/Thiagojm/rng_go_cli/pseudorng"
)

func main() {
	bits := flag.Int("bits", 1024, "number of bits to read per batch")
	interval := flag.Duration("interval", 0, "interval between reads (e.g. 2s). 0 for one-shot")
	flag.Parse()

	present, err := pseudorng.Detect()
	if err != nil {
		log.Fatalf("detect error: %v", err)
	}
	if !present {
		log.Fatal("pseudorng not available")
	}

	if *interval == 0 {
		data, err := pseudorng.ReadBits(*bits)
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
	err = pseudorng.CollectBitsAtInterval(ctx, *bits, *interval, func(b []byte) {
		fmt.Printf("%s  %d bits  %s\n", time.Now().Format(time.RFC3339), *bits, hex.EncodeToString(b))
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("collect error: %v", err)
	}
}
