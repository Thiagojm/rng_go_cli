## truerng package

Utilities to detect and read random data from a TrueRNG USB device exposed as a serial (COM) port on Windows. Designed for reuse as a library (API) in other Go applications, including GUIs.

### Import

```go
import "trng3_go_win/truerng"
```

If you're consuming this package from another local project and the module path `trng3_go_win` is not hosted remotely, add a replace directive in your consuming project's `go.mod`:

```go
require trng3_go_win v0.0.0

replace trng3_go_win => ../path/to/this/repo
```

Then run `go mod tidy`.

### Detecting the device

```go
present, err := truerng.Detect()
if err != nil { /* handle */ }
if !present { /* handle not found */ }
```

You can also resolve the COM port directly:

```go
port, err := truerng.FindPort() // e.g. "COM5"
```

### Reading bytes/bits (one-shot)

```go
// Read exact number of bytes
data, err := truerng.ReadBytes(64) // 64 bytes

// Read N bits (MSB-first within each byte). Last byte may be partially filled.
bits, err := truerng.ReadBits(2048) // 2048 bits (256 bytes)
```

### Reading at an interval

```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

err := truerng.CollectBitsAtInterval(ctx, 4096, 2*time.Second, func(b []byte) {
    // consume 4096 bits (packed in bytes)
})
```

### Behavior and notes
- Detection mirrors the Python approach by matching a `TrueRNG` prefix in device descriptors, with additional VID/PID hints.
- When reading:
  - DTR is asserted and input buffer is flushed before reads.
  - A high baud rate is requested (3,000,000); the OS/driver will clamp as needed.
  - An overall 10s read deadline prevents indefinite blocking.
- Bits are packed MSB-first in each byte; if the requested bit count is not a multiple of 8, unused trailing bits in the final byte are zeroed.


