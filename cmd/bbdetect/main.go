package main

import (
	"fmt"
	"os"

	"github.com/Thiagojm/rng_go_cli/bbusb"
)

func main() {
	ok, devices, err := bbusb.IsBitBabblerConnected()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if !ok {
		fmt.Println("No BitBabbler devices found (VID 0x0403 PID 0x7840)")
		return
	}
	for i, d := range devices {
		fmt.Printf("Device %d:\n", i+1)
		if d.FriendlyName != "" {
			fmt.Printf("  Name: %s\n", d.FriendlyName)
		}
		if d.DevicePath != "" {
			fmt.Printf("  Path: %s\n", d.DevicePath)
		}
		if len(d.HardwareIDs) > 0 {
			fmt.Printf("  HWIDs:\n")
			for _, h := range d.HardwareIDs {
				fmt.Printf("    %s\n", h)
			}
		}
	}
}
