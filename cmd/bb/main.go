package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/Thiagojm/rng_go_cli/bbusb"
)

func main() {
	bitsFlag := flag.Int("bits", 0, "number of bits to read from device")
	timeoutFlag := flag.Duration("timeout", 3*time.Second, "read timeout")
	flag.Parse()

	ok, devices, err := bbusb.IsBitBabblerConnected()
	if err != nil {
		fmt.Fprintf(os.Stderr, "detect error: %v\n", err)
		os.Exit(1)
	}
	if !ok {
		fmt.Println("No BitBabbler devices found (VID 0x0403 PID 0x7840)")
		os.Exit(1)
	}
	fmt.Printf("Found %d device(s). Using the first.\n", len(devices))
	if devices[0].FriendlyName != "" {
		fmt.Printf("Device: %s\n", devices[0].FriendlyName)
	}

	numBits := *bitsFlag
	if numBits <= 0 {
		fmt.Print("Enter number of bits to read: ")
		reader := bufio.NewReader(os.Stdin)
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		var n int
		fmt.Sscanf(line, "%d", &n)
		numBits = n
	}
	if numBits <= 0 {
		fmt.Fprintln(os.Stderr, "invalid bit count")
		os.Exit(1)
	}

	// Round bits up to bytes
	numBytes := (numBits + 7) / 8

	sess, err := bbusb.OpenBitBabbler(2_500_000, 1)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open error: %v\n", err)
		os.Exit(1)
	}
	defer sess.Close()

	buf := make([]byte, numBytes)
	ctx, cancel := context.WithTimeout(context.Background(), *timeoutFlag)
	defer cancel()
	n, err := sess.ReadRandom(ctx, buf)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read error: %v\n", err)
		os.Exit(1)
	}
	if n < numBytes {
		buf = buf[:n]
	}

	// Trim excess bits from the last byte if needed
	excess := (8 - (numBits % 8)) % 8
	if excess != 0 && len(buf) > 0 {
		buf[len(buf)-1] &= 0xFF >> excess
	}

	// Hex
	fmt.Printf("HEX: %x\n", buf)

	// Binary
	var sb strings.Builder
	for i, b := range buf {
		if i == len(buf)-1 && excess != 0 {
			fmt.Fprintf(&sb, "%0*b", 8-excess, b)
		} else {
			fmt.Fprintf(&sb, "%08b", b)
		}
	}
	// Trim to exact bits
	binStr := sb.String()
	if len(binStr) > numBits {
		binStr = binStr[:numBits]
	}
	fmt.Printf("BIN: %s\n", binStr)

	// Integer (big-endian)
	bi := new(big.Int).SetBytes(buf)
	fmt.Printf("INT: %s\n", bi.String())
}
