package bbusb

import (
	"context"
	"errors"
	"time"
)

// IsPresent returns whether a BitBabbler device is connected and its device list.
// It wraps IsBitBabblerConnected for convenience in GUI apps.
func IsPresent() (bool, []DeviceInfo, error) {
	return IsBitBabblerConnected()
}

// ReadBitsOnce opens the device (if present), reads the requested number of bits,
// and returns the data as bytes. The last byte is masked so the buffer contains
// exactly the requested number of bits (MSB-first in each byte).
//
// Parameters:
// - bitrate: MPSSE clock in Hz (e.g., 2_500_000). If 0, a conservative default is used.
// - latencyMs: FTDI latency timer (1-255). If 0, a conservative default is used.
func ReadBitsOnce(ctx context.Context, bits int, bitrate uint, latencyMs uint8) ([]byte, error) {
	if bits <= 0 {
		return nil, errors.New("bits must be > 0")
	}
	sess, err := OpenBitBabbler(bitrate, latencyMs)
	if err != nil {
		return nil, err
	}
	defer sess.Close()

	numBytes := (bits + 7) / 8
	buf := make([]byte, numBytes)
	got, err := sess.ReadRandom(ctx, buf)
	if err != nil {
		return nil, err
	}
	if got < numBytes {
		buf = buf[:got]
	}
	// Mask excess bits in the final byte to exactly match the requested bit count
	if bits%8 != 0 && len(buf) > 0 {
		excess := 8 - (bits % 8)
		buf[len(buf)-1] &= 0xFF >> excess
	}
	return buf, nil
}

// ReadResult represents the outcome of a periodic read.
type ReadResult struct {
	// When the read completed.
	Timestamp time.Time
	// Number of bits the collector attempted to read.
	BitsRequested int
	// Data contains ceiling(BitsRequested/8) bytes with the last byte masked
	// so the total bits equal BitsRequested.
	Data []byte
	// Err is non-nil if the read failed.
	Err error
}

// StartBitCollector opens the device once and performs periodic reads of the
// specified number of bits at the given interval, sending each result on a channel.
// The returned channel is closed when ctx is cancelled or the device session closes.
//
// Parameters:
// - bits: number of bits to read each cycle
// - interval: delay between cycles
// - bitrate, latencyMs: forwarded to OpenBitBabbler; pass 0 for defaults
func StartBitCollector(ctx context.Context, bits int, interval time.Duration, bitrate uint, latencyMs uint8) (<-chan ReadResult, error) {
	if bits <= 0 {
		return nil, errors.New("bits must be > 0")
	}
	if interval <= 0 {
		return nil, errors.New("interval must be > 0")
	}

	sess, err := OpenBitBabbler(bitrate, latencyMs)
	if err != nil {
		return nil, err
	}

	out := make(chan ReadResult)
	numBytes := (bits + 7) / 8

	go func() {
		defer close(out)
		defer sess.Close()

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			// Perform one read
			buf := make([]byte, numBytes)
			n, err := sess.ReadRandom(ctx, buf)
			if err == nil && n < numBytes {
				buf = buf[:n]
			}
			if err == nil && bits%8 != 0 && len(buf) > 0 {
				excess := 8 - (bits % 8)
				buf[len(buf)-1] &= 0xFF >> excess
			}

			select {
			case out <- ReadResult{Timestamp: time.Now(), BitsRequested: bits, Data: buf, Err: err}:
			case <-ctx.Done():
				return
			}

			select {
			case <-ticker.C:
				continue
			case <-ctx.Done():
				return
			}
		}
	}()

	return out, nil
}
