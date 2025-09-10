# rng_go_cli

Random data collection CLI in Go for three sources:
- BitBabbler USB (hardware)
- TrueRNG3 (hardware)
- Pseudorandom (software)

It collects N bits every M seconds until stopped, streaming to:
- a .bin file containing the raw bytes
- a .csv file containing the timestamp and count of ones in each sample

## Requirements
- Go (module: `github.com/Thiagojm/rng_go_cli`)
- Windows supported (others may work with suitable drivers)
- For BitBabbler:
  - `libusb-1.0.dll` available in your working directory or on PATH
  - If detection fails, place the provided `libusb-1.0.dll` in the repo root or add its folder to PATH
    - Install MSYS2 (C:\msys64) and open “MSYS2 MinGW x64” shell, then:
    ```powershell
      pacman -Syu
      pacman -S mingw-w64-x86_64-toolchain mingw-w64-x86_64-libusb mingw-w64-x86_64-pkg-config
      ```
- For TrueRNG3:
  - Proper serial drivers installed (the CLI auto-detects by port description)

## Install / Build
From repo root:
```powershell
# Build the main collector
go build -o collect.exe ./cmd/collect

# Build optimized
go build -ldflags="-s -w" ./cmd/collect

# Or run without building
go run ./cmd/collect -device pseudo -bits 2048 -interval 1 -outdir data
```

Other sample CLIs:
```powershell
# Pseudorandom demo
go run ./cmd/pseudocli -bits 1024 -interval 1s

# TrueRNG demo (one-shot/interval)
go run ./cmd/trngcli -bits 1024 -interval 0
```

## Collector CLI
Command: `./cmd/collect`

Flags:
- `-device` (string): `pseudo` | `trng` | `bitb`
- `-bits` (int): number of bits per sample (> 0)
- `-interval` (int): interval in seconds between samples (> 0)
- `-outdir` (string): output directory (default `data`)

Examples:
```powershell
# Pseudorandom, 2048 bits each 1s
go run ./cmd/collect -device pseudo -bits 2048 -interval 1 -outdir data

# TrueRNG3, 1024 bits each 2s
go run ./cmd/collect -device trng -bits 1024 -interval 2 -outdir data

# BitBabbler, 4096 bits each 1s (ensure libusb-1.0.dll is available)
go run ./cmd/collect -device bitb -bits 4096 -interval 1 -outdir data
```

Runtime output:
- Prints per-sample progress to the console, e.g.
```
sample 3: ones=1021/2048 at 20250910T17:29:02
```

## File Naming Convention
Files are named using local time:
```
YYYYMMDDTHHMMSS_{device}_s{bits}_i{interval}
```
Where `device` ∈ {`trng`, `bitb`, `pseudo`}.

Examples:
- `20201011T142208_bitb_s2048_i1.bin`
- `20201011T142208_bitb_s2048_i1.csv`

Implemented by `naming.BuildBaseName` and helpers in `naming/`.

## CSV Format
Each line: `YYYYMMDDTHH:MM:SS,<ones_count>`
- Timestamp is local time
- `<ones_count>` is the number of set bits within the requested sample size

Example lines:
```
20250910T14:45:40,1028
20250910T14:45:41,1007
```

## Pseudorandom API
Package: `pseudorng`
```go
b, _ := pseudorng.ReadBits(2048)
_ = pseudorng.CollectBitsAtInterval(ctx, 1024, 1*time.Second, func(batch []byte) { /* ... */ })
```
Deterministic generator:
```go
g, _ := pseudorng.NewGenerator(12345)
b2, _ := g.ReadBits(512)
```

## TrueRNG / BitBabbler Notes
- TrueRNG detection is automatic; the tool will exit if no device is found
- BitBabbler detection is performed before opening; missing `libusb-1.0.dll` will raise an open error

## Troubleshooting
- BitBabbler: `libusb: not found` → ensure `libusb-1.0.dll` is in the repo root or on PATH
- TrueRNG not found → verify the device appears as a serial port and drivers are installed
- Permission issues → run terminal with sufficient privileges

## Project Layout (key parts)
- `cmd/collect`: main collector CLI
- `cmd/trngcli`, `cmd/pseudocli`: sample CLIs
- `bbusb`: BitBabbler access (USB/libusb)
- `truerng`: TrueRNG (serial) access
- `pseudorng`: software PRNG implementation
- `naming`: filename convention helpers

## License
See `LICENSE.txt`.


