//go:build windows

package bbusb

import (
	"context"
	"errors"
	"time"

	"github.com/google/gousb"
)

// FTDI vendor/product for BitBabbler
const (
	ftdiVendorID = 0x0403
	bbProductID  = 0x7840
)

// mpsse constants mirrors
const (
	mpsseNoClkDiv5     = 0x8A
	mpsseNoAdaptiveClk = 0x97
	mpsseNo3PhaseClk   = 0x8D
	mpsseSetDataLow    = 0x80
	mpsseSetDataHigh   = 0x82
	mpsseSetClkDivisor = 0x86
	mpsseSendImmediate = 0x87

	// read bytes in, MSB first, sample on +ve edge (matches default vendor code path)
	mpsseDataByteInPosMSB = 0x20
)

// ftdi SIO requests (vendor-specific)
const (
	ftdiReqReset        = 0x00
	ftdiReqSetFlowCtrl  = 0x02
	ftdiReqSetBaudRate  = 0x03
	ftdiReqSetData      = 0x04
	ftdiReqGetModemStat = 0x05
	ftdiReqSetEventChar = 0x06
	ftdiReqSetErrorChar = 0x07
	ftdiReqSetLatency   = 0x09
	ftdiReqGetLatency   = 0x0A
	ftdiReqSetBitmode   = 0x0B
)

// ftdi reset values
const (
	ftdiResetSIO     = 0
	ftdiResetPurgeRX = 1
	ftdiResetPurgeTX = 2
)

// ftdi flow control
const (
	ftdiFlowNone   = 0x0000
	ftdiFlowRtsCts = 0x0100
)

// ftdi bitmodes
const (
	ftdiBitmodeReset = 0x0000
	ftdiBitmodeMpsse = 0x0200
)

// DeviceSession encapsulates an open BitBabbler FTDI device via gousb.
//
// Usage:
//
//	s, _ := OpenBitBabbler(2_500_000, 1)
//	defer s.Close()
//	buf := make([]byte, 4096)
//	_, _ = s.ReadRandom(context.Background(), buf)
type DeviceSession struct {
	ctx       *gousb.Context
	dev       *gousb.Device
	cfg       *gousb.Config
	intf      *gousb.Interface
	inEp      *gousb.InEndpoint
	outEp     *gousb.OutEndpoint
	maxPacket int
}

// OpenBitBabbler opens the first BitBabbler FTDI device and initializes MPSSE.
// bitrate: desired bit clock; vendor defaults pick 2_500_000 if 0.
// latencyMs: FTDI latency timer; vendor default is 1ms if 0.
func OpenBitBabbler(bitrate uint, latencyMs uint8) (*DeviceSession, error) {
	if bitrate == 0 {
		bitrate = 2_500_000
	}
	if latencyMs == 0 {
		latencyMs = 1
	}

	ctx := gousb.NewContext()
	// optional: ctx.Debug(1)

	dev, err := ctx.OpenDeviceWithVIDPID(gousb.ID(ftdiVendorID), gousb.ID(bbProductID))
	if err != nil {
		ctx.Close()
		return nil, err
	}
	if dev == nil {
		ctx.Close()
		return nil, errors.New("BitBabbler device not found")
	}

	// Ensure it's auto-detached from kernel drivers where applicable
	_ = dev.SetAutoDetach(true)

	cfg, err := dev.Config(1)
	if err != nil {
		dev.Close()
		ctx.Close()
		return nil, err
	}
	intf, err := cfg.Interface(0, 0)
	if err != nil {
		cfg.Close()
		dev.Close()
		ctx.Close()
		return nil, err
	}

	// Find bulk endpoints (expect 1 IN, 1 OUT)
	var inEp *gousb.InEndpoint
	var outEp *gousb.OutEndpoint
	for _, ep := range intf.Setting.Endpoints {
		if ep.Direction == gousb.EndpointDirectionIn && ep.TransferType == gousb.TransferTypeBulk {
			inEp, err = intf.InEndpoint(ep.Number)
			if err != nil {
				intf.Close()
				cfg.Close()
				dev.Close()
				ctx.Close()
				return nil, err
			}
		}
		if ep.Direction == gousb.EndpointDirectionOut && ep.TransferType == gousb.TransferTypeBulk {
			outEp, err = intf.OutEndpoint(ep.Number)
			if err != nil {
				intf.Close()
				cfg.Close()
				dev.Close()
				ctx.Close()
				return nil, err
			}
		}
	}
	if inEp == nil || outEp == nil {
		intf.Close()
		cfg.Close()
		dev.Close()
		ctx.Close()
		return nil, errors.New("bulk endpoints not found")
	}

	s := &DeviceSession{ctx: ctx, dev: dev, cfg: cfg, intf: intf, inEp: inEp, outEp: outEp, maxPacket: int(inEp.Desc.MaxPacketSize)}

	// Follow vendor InitMPSSE sequence
	if err := s.ftdiReset(); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.purgeRead(); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.ftdiSetSpecialChars(0, false, 0, false); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.ftdiSetLatencyTimer(latencyMs); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.ftdiSetFlowControl(ftdiFlowRtsCts); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.ftdiSetBitmode(ftdiBitmodeReset, 0); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.ftdiSetBitmode(ftdiBitmodeMpsse, 0); err != nil {
		s.Close()
		return nil, err
	}
	time.Sleep(50 * time.Millisecond)

	// Sync check AA/AB sequence (retry once if the first attempt fails)
	ok := s.checkSync(0xAA) && s.checkSync(0xAB)
	if !ok {
		ok = s.checkSync(0xAA) && s.checkSync(0xAB)
	}
	if !ok {
		s.Close()
		return nil, errors.New("MPSSE sync failed")
	}

	// Program device per init_device: disable div/3phase/adaptive, set pins, set clock
	clkDiv := uint16(30000000/bitrate - 1)
	cmd := []byte{
		mpsseNoClkDiv5,
		mpsseNoAdaptiveClk,
		mpsseNo3PhaseClk,
		mpsseSetDataLow,
		0x00, // outputs low, polarity mask configurable later if needed
		0x0B, // direction: CLK, DO, CS outputs
		mpsseSetDataHigh,
		0x00, // high pins low
		0x00, // high pins as inputs
		mpsseSetClkDivisor,
		byte(clkDiv & 0xFF),
		byte(clkDiv >> 8),
		0x85, // NO loopback
	}
	if _, err := s.outEp.Write(cmd); err != nil {
		s.Close()
		return nil, err
	}
	time.Sleep(30 * time.Millisecond)
	// Clear any zero-length response
	_ = s.purgeRead()

	return s, nil
}

// Close releases USB resources.
func (s *DeviceSession) Close() {
	if s == nil {
		return
	}
	if s.intf != nil {
		s.intf.Close()
	}
	if s.cfg != nil {
		s.cfg.Close()
	}
	if s.dev != nil {
		s.dev.Close()
	}
	if s.ctx != nil {
		s.ctx.Close()
	}
}

// ReadRandom fills buf with random data from device. It issues an MPSSE read command
// and drains the FTDI 2-byte status headers across packets.
func (s *DeviceSession) ReadRandom(ctx context.Context, buf []byte) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}
	// Issue MPSSE read command: read len bytes
	n := len(buf)
	cmd := []byte{
		mpsseDataByteInPosMSB,
		byte((n - 1) & 0xFF),
		byte((n - 1) >> 8),
		mpsseSendImmediate,
	}
	if _, err := s.outEp.Write(cmd); err != nil {
		return 0, err
	}

	// Read back, stripping 2-byte status per packet
	want := n
	got := 0
	tmp := make([]byte, roundUpToMaxPacket(n, s.maxPacket)+s.maxPacket)
	for got < want {
		m, err := s.inEp.Read(tmp)
		if err != nil {
			return got, err
		}
		if m <= 2 {
			continue
		}
		// Compact by skipping first 2 bytes of each packet-sized chunk
		offset := 0
		for offset < m {
			remain := m - offset
			if remain <= 2 {
				break
			}
			take := remain
			if take > s.maxPacket {
				take = s.maxPacket
			}
			usable := take - 2
			if usable > (want - got) {
				usable = want - got
			}
			copy(buf[got:got+usable], tmp[offset+2:offset+2+usable])
			got += usable
			offset += take
			if got == want {
				break
			}
		}
	}
	return got, nil
}

// Helpers

func (s *DeviceSession) control(req uint8, value uint16, index uint16, data []byte, in bool) error {
	// bmRequestType fields combined
	typ := uint8(gousb.ControlOut) | uint8(gousb.ControlVendor) | uint8(gousb.ControlDevice)
	if in {
		typ = uint8(gousb.ControlIn) | uint8(gousb.ControlVendor) | uint8(gousb.ControlDevice)
	}
	_, err := s.dev.Control(uint8(typ), req, value, index, data)
	return err
}

func (s *DeviceSession) ftdiReset() error {
	return s.control(ftdiReqReset, ftdiResetSIO, 1, nil, false)
}
func (s *DeviceSession) ftdiSetBitmode(mode uint16, mask uint8) error {
	return s.control(ftdiReqSetBitmode, mode|uint16(mask), 1, nil, false)
}
func (s *DeviceSession) ftdiSetLatencyTimer(ms uint8) error {
	return s.control(ftdiReqSetLatency, uint16(ms), 1, nil, false)
}
func (s *DeviceSession) ftdiSetFlowControl(mode uint16) error {
	return s.control(ftdiReqSetFlowCtrl, 0, mode|1, nil, false)
}
func (s *DeviceSession) ftdiSetSpecialChars(event byte, evtEnable bool, errc byte, errEnable bool) error {
	v := uint16(event)
	if evtEnable {
		v |= 0x0100
	}
	if err := s.control(ftdiReqSetEventChar, v, 1, nil, false); err != nil {
		return err
	}
	v = uint16(errc)
	if errEnable {
		v |= 0x0100
	}
	return s.control(ftdiReqSetErrorChar, v, 1, nil, false)
}

func (s *DeviceSession) purgeRead() error {
	buf := make([]byte, 8192)
	for i := 0; i < 10; i++ {
		n, _ := s.inEp.Read(buf)
		if n <= 2 {
			break
		}
	}
	return nil
}

func (s *DeviceSession) checkSync(cmd byte) bool {
	msg := []byte{cmd, mpsseSendImmediate}
	if _, err := s.outEp.Write(msg); err != nil {
		return false
	}
	buf := make([]byte, 512)
	for i := 0; i < 10; i++ {
		n, _ := s.inEp.Read(buf)
		if n == 4 && buf[2] == 0xFA && buf[3] == cmd {
			return true
		}
	}
	return false
}

func roundUpToMaxPacket(n, max int) int {
	if max <= 0 {
		return n
	}
	if n%max == 0 {
		return n
	}
	return (n/max + 1) * max
}
