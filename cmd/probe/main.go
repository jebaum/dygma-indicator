package main

import (
	"fmt"
	"log"
	"os"
	"slices"
	"time"

	"go.bug.st/serial"
	"go.bug.st/serial/enumerator"
)

var vendorIds = []string{"1209", "35ef"}

func findKeyboardDev() (string, error) {
	ports, err := enumerator.GetDetailedPortsList()
	if err != nil {
		return "", err
	}
	for _, port := range ports {
		if slices.Contains(vendorIds, port.VID) {
			return port.Name, nil
		}
	}
	return "", fmt.Errorf("no Dygma keyboard found")
}

// sendAndRead writes cmd to the port and reads everything available until
// the port stays idle for `quiet` duration (or `total` elapses). It returns
// the raw bytes received so we can see exactly what the firmware emits.
func sendAndRead(port serial.Port, cmd string, quiet, total time.Duration) ([]byte, error) {
	if err := port.SetReadTimeout(quiet); err != nil {
		return nil, err
	}
	if _, err := port.Write([]byte(cmd + "\n")); err != nil {
		return nil, err
	}

	deadline := time.Now().Add(total)
	var out []byte
	buf := make([]byte, 256)
	for time.Now().Before(deadline) {
		n, err := port.Read(buf)
		if err != nil {
			return out, err
		}
		if n == 0 {
			// Idle for `quiet` with nothing more arriving — done.
			break
		}
		out = append(out, buf[:n]...)
	}
	return out, nil
}

func main() {
	dev, err := findKeyboardDev()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Fprintf(os.Stderr, "Using device: %s\n\n", dev)

	port, err := serial.Open(dev, &serial.Mode{BaudRate: 9600})
	if err != nil {
		log.Fatal(err)
	}
	defer port.Close()

	commands := []string{
		"help",
		"version",
		"wireless.battery.left.level",
		"wireless.battery.right.level",
		"wireless.battery.left.status",
		"wireless.battery.right.status",
		"wireless.battery.savingMode",
		"wireless.battery.left.voltage",
		"wireless.battery.right.voltage",
		"hardware.side_power",
		"hardware.side_ver",
		"hardware.chip_id",
		"hardware.chip_id.left",
		"hardware.chip_id.left_rf",
		"hardware.chip_id.right",
		"hardware.chip_id.right_rf",
		"hardware.chip_info",
		"hardware.firmware",
		"hardware.version",
		"hardware.wireless",
		"wireless.rf.power",
		"wireless.rf.channelHop",
		"wireless.rf.syncPairing",
		"upgrade.keyscanner.isConnected",
	}

	for _, cmd := range commands {
		fmt.Printf("=== %s ===\n", cmd)
		resp, err := sendAndRead(port, cmd, 200*time.Millisecond, 2*time.Second)
		if err != nil {
			fmt.Printf("  error: %v\n", err)
		}
		fmt.Printf("  raw : %q\n", string(resp))
		fmt.Printf("  hex : % x\n\n", resp)
	}
}
