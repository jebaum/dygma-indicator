package main

import (
	"fmt"
	"log"
	"os"

	"dygma-indicator/internal/neuron"
)

func main() {
	dev, err := neuron.FindDev()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Fprintf(os.Stderr, "Using device: %s\n\n", dev)

	client, err := neuron.Open(dev)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()
	client.SetDebug(os.Stdout)

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
			fmt.Printf("  error: %v\n", err)
		}
		fmt.Printf("  %s\n\n", resp)
	}
}
