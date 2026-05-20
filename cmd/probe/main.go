package main

import (
	"fmt"
	"os"

	"dygma-indicator/internal/neuron"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	dev, err := neuron.FindDev()
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Using device: %s\n\n", dev)

	client, err := neuron.Open(dev)
	if err != nil {
		return err
	}
	defer client.Close()
	client.SetDebug(os.Stderr)

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
		resp, err := client.Query(cmd)
		if err != nil {
			fmt.Printf("  error: %v\n\n", err)
			break
		}
		fmt.Printf("  %s\n\n", resp)
	}
	return nil
}
