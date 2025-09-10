package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/bits"
	"os"
	"os/signal"
	"time"

	"github.com/Thiagojm/rng_go_cli/bbusb"
	"github.com/Thiagojm/rng_go_cli/naming"
	"github.com/Thiagojm/rng_go_cli/pseudorng"
	"github.com/Thiagojm/rng_go_cli/truerng"
)

// countOnes returns the number of set bits in buf, considering only bitCount bits total.
// The final byte is handled so that unused trailing bits (if any) are not counted.
func countOnes(buf []byte, bitCount int) int {
	if bitCount <= 0 || len(buf) == 0 {
		return 0
	}
	bytesUsed := (bitCount + 7) / 8
	if bytesUsed > len(buf) {
		bytesUsed = len(buf)
	}
	// Count all full bytes except possibly the last.
	total := 0
	for i := 0; i < bytesUsed-1; i++ {
		total += bits.OnesCount8(buf[i])
	}
	// Handle last byte respecting bitCount.
	if bytesUsed > 0 {
		usedBitsInLast := bitCount - (bytesUsed-1)*8
		if usedBitsInLast <= 0 {
			usedBitsInLast = 8
		}
		mask := byte(0xFF) << (8 - usedBitsInLast)
		last := buf[bytesUsed-1] & mask
		total += bits.OnesCount8(last)
	}
	return total
}

func main() {
	bitsFlag := flag.Int("bits", 2048, "number of bits per batch (required > 0)")
	intervalSec := flag.Int("interval", 1, "interval between batches in seconds (required > 0)")
	deviceFlag := flag.String("device", "pseudo", "device to read from: pseudo|trng|bitb")
	outDir := flag.String("outdir", "data", "output directory for files")
	flag.Parse()

	if *bitsFlag <= 0 {
		log.Fatal("-bits must be > 0")
	}
	if *intervalSec <= 0 {
		log.Fatal("-interval must be > 0")
	}

	// Map device flag to naming.Device
	var dev naming.Device
	switch *deviceFlag {
	case string(naming.DevicePseudo):
		dev = naming.DevicePseudo
	case string(naming.DeviceTrueRNG):
		dev = naming.DeviceTrueRNG
	case string(naming.DeviceBitBabbler):
		dev = naming.DeviceBitBabbler
	default:
		log.Fatalf("invalid -device: %s (allowed: pseudo, trng, bitb)", *deviceFlag)
	}

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Fatalf("creating outdir: %v", err)
	}

	startTime := time.Now()
	binPath, csvPath, err := naming.BuildBinCSVPaths(*outDir, startTime, dev, *bitsFlag, *intervalSec)
	if err != nil {
		log.Fatalf("build filenames: %v", err)
	}

	binFile, err := os.OpenFile(binPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		log.Fatalf("open bin file: %v", err)
	}
	defer func() { _ = binFile.Close() }()
	binBuf := bufio.NewWriter(binFile)
	defer binBuf.Flush()

	csvFile, err := os.OpenFile(csvPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		log.Fatalf("open csv file: %v", err)
	}
	defer func() { _ = csvFile.Close() }()
	csvBuf := bufio.NewWriter(csvFile)
	defer csvBuf.Flush()

	// Prepare a bitreader function for the chosen device.
	bitCount := *bitsFlag
	byteCount := (bitCount + 7) / 8
	var readBits func(context.Context) ([]byte, error)

	switch dev {
	case naming.DevicePseudo:
		readBits = func(ctx context.Context) ([]byte, error) {
			return pseudorng.ReadBits(bitCount)
		}
	case naming.DeviceTrueRNG:
		// Sanity check presence
		present, derr := truerng.Detect()
		if derr != nil {
			log.Fatalf("trng detect: %v", derr)
		}
		if !present {
			log.Fatal("TrueRNG device not found")
		}
		readBits = func(ctx context.Context) ([]byte, error) {
			return truerng.ReadBits(bitCount)
		}
	case naming.DeviceBitBabbler:
		// Check presence first for clearer errors
		ok, devices, derr := bbusb.IsBitBabblerConnected()
		if derr != nil {
			log.Fatalf("bitb detect: %v", derr)
		}
		if !ok {
			log.Fatal("No BitBabbler devices found (VID 0x0403 PID 0x7840)")
		}
		sess, oerr := bbusb.OpenBitBabbler(2_500_000, 1)
		if oerr != nil {
			log.Fatalf("bitb open: %v (ensure libusb-1.0.dll is available)", oerr)
		}
		defer sess.Close()
		if len(devices) > 0 && devices[0].FriendlyName != "" {
			log.Printf("using BitBabbler: %s", devices[0].FriendlyName)
		}
		readBits = func(ctx context.Context) ([]byte, error) {
			buf := make([]byte, byteCount)
			// Short per-read timeout to avoid hanging.
			ct, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			n, rerr := sess.ReadRandom(ct, buf)
			if rerr != nil {
				return nil, rerr
			}
			if n < len(buf) {
				buf = buf[:n]
			}
			// Zero trailing unused bits so counting is consistent.
			extra := (8 - (bitCount % 8)) % 8
			if extra != 0 && len(buf) > 0 {
				buf[len(buf)-1] &= 0xFF >> extra
			}
			return buf, nil
		}
	default:
		log.Fatal("unsupported device")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	interval := time.Duration(*intervalSec) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	log.Printf("collecting %d bits every %s from %s", bitCount, interval.String(), string(dev))
	sampleNum := 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		batch, rerr := readBits(ctx)
		if rerr != nil {
			// Stop on read error
			if !errors.Is(rerr, context.Canceled) {
				log.Printf("read error: %v", rerr)
			}
			return
		}

		// Write raw bytes to .bin
		if _, werr := binBuf.Write(batch); werr != nil {
			log.Fatalf("write bin: %v", werr)
		}
		_ = binBuf.Flush()

		// Compute ones across the intended bitCount
		ones := countOnes(batch, bitCount)
		sampleNum++
		ts := time.Now().Format("20060102T15:04:05")
		if _, werr := fmt.Fprintf(csvBuf, "%s,%d\n", ts, ones); werr != nil {
			log.Fatalf("write csv: %v", werr)
		}
		_ = csvBuf.Flush()

		// Print progress to terminal
		fmt.Printf("sample %d: ones=%d/%d at %s\n", sampleNum, ones, bitCount, ts)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
