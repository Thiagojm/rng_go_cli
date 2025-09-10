package pseudorng

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"errors"
	mrand "math/rand"
	"time"
)

// Detect for pseudorng always returns true, since software RNG is always available.
func Detect() (bool, error) { return true, nil }

// ReadBits returns bitCount pseudorandom bits as bytes, MSB-first per byte.
// The final byte may be partially filled with zeros in the unused trailing bits.
func ReadBits(bitCount int) ([]byte, error) {
	if bitCount <= 0 {
		return nil, errors.New("bitCount must be positive")
	}
	byteCount := (bitCount + 7) / 8
	// Use crypto/rand as entropy for default generation, then fill with math/rand for speed if needed.
	buf := make([]byte, byteCount)
	if _, err := crand.Read(buf); err != nil {
		return nil, err
	}
	// Zero out unused trailing bits for clarity.
	extraBits := (8 - (bitCount % 8)) % 8
	if extraBits != 0 {
		mask := byte(0xFF << extraBits)
		buf[len(buf)-1] &= mask
	}
	return buf, nil
}

// CollectBitsAtInterval generates bitCount bits every interval and calls onBatch.
// Runs until ctx is cancelled; returns any error encountered.
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

// Generator is a deterministic PRNG wrapper that can be seeded for reproducible streams.
// It uses math/rand/v2 with a 64-bit seed; seed material can be derived from crypto/rand.
type Generator struct {
	r *mrand.Rand
}

// NewGenerator creates a new pseudorandom generator. If seed is zero, a random
// seed is drawn from crypto/rand.
func NewGenerator(seed uint64) (*Generator, error) {
	if seed == 0 {
		var s [8]byte
		if _, err := crand.Read(s[:]); err != nil {
			return nil, err
		}
		seed = binary.LittleEndian.Uint64(s[:])
	}
	return &Generator{r: mrand.New(mrand.NewSource(int64(seed)))}, nil
}

// ReadBits reads bitCount bits from the deterministic generator.
func (g *Generator) ReadBits(bitCount int) ([]byte, error) {
	if g == nil || g.r == nil {
		return nil, errors.New("generator is nil")
	}
	if bitCount <= 0 {
		return nil, errors.New("bitCount must be positive")
	}
	byteCount := (bitCount + 7) / 8
	buf := make([]byte, byteCount)
	for i := 0; i < byteCount; i++ {
		buf[i] = byte(g.r.Intn(256))
	}
	// Zero out unused trailing bits
	extraBits := (8 - (bitCount % 8)) % 8
	if extraBits != 0 {
		mask := byte(0xFF << extraBits)
		buf[len(buf)-1] &= mask
	}
	return buf, nil
}

// CollectBitsAtInterval runs the deterministic generator at a fixed interval.
func (g *Generator) CollectBitsAtInterval(ctx context.Context, bitCount int, interval time.Duration, onBatch func([]byte)) error {
	if g == nil || g.r == nil {
		return errors.New("generator is nil")
	}
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

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		b, err := g.ReadBits(bitCount)
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
