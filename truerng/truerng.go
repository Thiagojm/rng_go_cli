package truerng

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.bug.st/serial"
	"go.bug.st/serial/enumerator"
)

// DeviceNamePrefix is the prefix used in the device name/description to
// identify a TrueRNG serial device. Mirrors the Python logic that checks
// description starts with "TrueRNG".
const DeviceNamePrefix = "TrueRNG"

// Detect returns true if a TrueRNG serial device is present on the system.
// It enumerates available serial ports and checks their friendly name or
// description for a TrueRNG prefix.
func Detect() (bool, error) {
	ports, err := enumerator.GetDetailedPortsList()
	if err != nil {
		return false, fmt.Errorf("enumerating ports: %w", err)
	}
	for _, p := range ports {
		if p == nil {
			continue
		}
		if hasTrueRNGPrefix(p) {
			return true, nil
		}
	}
	return false, nil
}

// FindPort returns the first COM port path for a detected TrueRNG device, e.g.
// "COM5" on Windows.
func FindPort() (string, error) {
	ports, err := enumerator.GetDetailedPortsList()
	if err != nil {
		return "", fmt.Errorf("enumerating ports: %w", err)
	}
	for _, p := range ports {
		if p == nil {
			continue
		}
		if hasTrueRNGPrefix(p) {
			if p.Name != "" {
				return p.Name, nil
			}
		}
	}
	return "", errors.New("TrueRNG device not found")
}

// ReadBytes opens the TrueRNG serial port, sets DTR, flushes input, and reads
// blockSize bytes. The behavior mirrors `truerng.py`'s read_bytes.
func ReadBytes(blockSize int) ([]byte, error) {
	if blockSize <= 0 {
		return nil, errors.New("blockSize must be positive")
	}
	portName, err := FindPort()
	if err != nil {
		return nil, err
	}

	mode := &serial.Mode{
		BaudRate: 3000000, // TrueRNG models typically support high baud; OS will clamp if unsupported
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	}

	port, err := serial.Open(portName, mode)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", portName, err)
	}
	defer func() { _ = port.Close() }()

	// Set DTR true (as in Python), then flush any buffered input before reading.
	_ = port.SetDTR(true)
	_ = port.SetReadTimeout(1000 * time.Millisecond)
	if err := port.ResetInputBuffer(); err != nil {
		// not fatal, proceed
	}

	buf := make([]byte, blockSize)
	total := 0
	deadline := time.Now().Add(10 * time.Second) // match Python's 10s timeout intent
	for total < blockSize {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("read timeout after 10s: read %d/%d bytes", total, blockSize)
		}
		n, err := port.Read(buf[total:])
		if err != nil {
			return nil, fmt.Errorf("read error: %w", err)
		}
		total += n
		if n == 0 {
			time.Sleep(5 * time.Millisecond)
		}
	}
	return buf, nil
}

// ReadBits reads bitCount bits from the TrueRNG and returns them as a byte
// slice packed MSB-first in each byte. The final byte may be partially filled.
func ReadBits(bitCount int) ([]byte, error) {
	if bitCount <= 0 {
		return nil, errors.New("bitCount must be positive")
	}
	byteCount := (bitCount + 7) / 8
	data, err := ReadBytes(byteCount)
	if err != nil {
		return nil, err
	}
	// If bitCount is not a multiple of 8, zero out the unused trailing bits for clarity.
	extraBits := (8 - (bitCount % 8)) % 8
	if extraBits != 0 {
		mask := byte(0xFF << extraBits)
		data[len(data)-1] &= mask
	}
	return data, nil
}

// CollectBitsAtInterval reads bitCount bits every interval, invoking onBatch
// with the bytes each time. It runs until the context is cancelled or a read
// error occurs. Any error is returned.
func CollectBitsAtInterval(ctx context.Context, bitCount int, interval time.Duration, onBatch func([]byte)) error {
	if bitCount <= 0 {
		return errors.New("bitCount must be positive")
	}
	if interval <= 0 {
		return errors.New("interval must be positive")
	}
	if onBatch == nil {
		return errors.New("onBatch callback must not be nil")
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Do an immediate first read, then on each tick thereafter.
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		b, err := ReadBits(bitCount)
		if err != nil {
			return err
		}
		onBatch(b)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func hasTrueRNGPrefix(p *enumerator.PortDetails) bool {
	if p == nil {
		return false
	}
	if p.IsUSB && p.Product != "" && len(p.Product) >= len(DeviceNamePrefix) && p.Product[:len(DeviceNamePrefix)] == DeviceNamePrefix {
		return true
	}
	if p.IsUSB && p.SerialNumber != "" && len(p.SerialNumber) >= len(DeviceNamePrefix) && p.SerialNumber[:len(DeviceNamePrefix)] == DeviceNamePrefix {
		return true
	}
	if p.Name != "" && len(p.Name) >= len(DeviceNamePrefix) && p.Name[:len(DeviceNamePrefix)] == DeviceNamePrefix {
		return true
	}
	if p.VID == "16D0" && (p.PID == "0AA0" || p.PID == "0AA2" || p.PID == "0AA4") { // Common TrueRNG VIDs/PIDs
		return true
	}
	return false
}
