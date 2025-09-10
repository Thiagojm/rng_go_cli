package naming

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// Device represents the data source used to collect random bits.
// Allowed values are: "trng" (TrueRNG3), "bitb" (BitBabbler), and "pseudo" (PRNG).
type Device string

const (
	DeviceTrueRNG    Device = "trng"
	DeviceBitBabbler Device = "bitb"
	DevicePseudo     Device = "pseudo"
)

// Validate checks whether d is one of the allowed device identifiers.
func (d Device) Validate() error {
	if d == DeviceTrueRNG || d == DeviceBitBabbler || d == DevicePseudo {
		return nil
	}
	return fmt.Errorf("invalid device: %q (allowed: trng, bitb, pseudo)", string(d))
}

// BuildBaseName builds the base filename using the convention:
//
//	YYYYMMDDTHHMMSS_{device}_s{bits}_i{interval}
//
// where:
// - device ∈ {trng, bitb, pseudo}
// - bits > 0 is the sample size in bits per collection
// - interval > 0 is the interval in seconds between collections
// The timestamp is generated from the provided time instant.
func BuildBaseName(now time.Time, device Device, bits int, intervalSeconds int) (string, error) {
	if err := device.Validate(); err != nil {
		return "", err
	}
	if bits <= 0 {
		return "", errors.New("bits must be > 0")
	}
	if intervalSeconds <= 0 {
		return "", errors.New("intervalSeconds must be > 0")
	}
	stamp := now.Format("20060102T150405")
	return fmt.Sprintf("%s_%s_s%d_i%d", stamp, string(device), bits, intervalSeconds), nil
}

// WithExt appends an extension (without leading dot) to a base name.
// If ext contains a leading dot, it is preserved once. Empty ext returns base.
func WithExt(base string, ext string) string {
	if ext == "" {
		return base
	}
	extClean := ext
	if strings.HasPrefix(ext, ".") {
		extClean = strings.TrimPrefix(ext, ".")
	}
	return base + "." + extClean
}

// JoinDir builds a path joining an optional directory with the filename.
// If dir is empty, it returns name as-is.
func JoinDir(dir string, name string) string {
	if dir == "" {
		return name
	}
	return filepath.Join(dir, name)
}

// BuildBinCSVNames builds both .bin and .csv filenames (without directory) based on the convention.
func BuildBinCSVNames(now time.Time, device Device, bits int, intervalSeconds int) (binName string, csvName string, err error) {
	base, err := BuildBaseName(now, device, bits, intervalSeconds)
	if err != nil {
		return "", "", err
	}
	return WithExt(base, ".bin"), WithExt(base, ".csv"), nil
}

// BuildBinCSVPaths builds full paths for .bin and .csv inside dir (dir may be empty).
func BuildBinCSVPaths(dir string, now time.Time, device Device, bits int, intervalSeconds int) (binPath string, csvPath string, err error) {
	binName, csvName, err := BuildBinCSVNames(now, device, bits, intervalSeconds)
	if err != nil {
		return "", "", err
	}
	return JoinDir(dir, binName), JoinDir(dir, csvName), nil
}
