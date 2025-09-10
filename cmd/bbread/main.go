package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Thiagojm/rng_go_cli/bbusb"
)

func main() {
	s, err := bbusb.OpenBitBabbler(2_500_000, 1)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open: %v\n", err)
		os.Exit(1)
	}
	defer s.Close()

	buf := make([]byte, 4096)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	n, err := s.ReadRandom(ctx, buf)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("read %d bytes\n", n)
	// Print first 32 bytes hex
	if n > 32 {
		n = 32
	}
	for i := 0; i < n; i++ {
		fmt.Printf("%02x", buf[i])
	}
	fmt.Println()
}
