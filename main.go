package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"

	"dygma-indicator/internal/neuron"
)

// sideStatus mirrors `wireless.battery.<side>.status` returned by the firmware.
// 0 = discharging, 1 = charging, 4 = unreachable (RF off / out of range).
// Any other value is treated as discharging so we still surface the level.
type sideStatus int

const (
	statusDischarging  sideStatus = 0
	statusCharging     sideStatus = 1
	statusDisconnected sideStatus = 4
)

type batteryLevel struct {
	Left, Right             int
	LeftStatus, RightStatus sideStatus
}

type WaybarOutput struct {
	Text       string `json:"text"`
	Tooltip    string `json:"tooltip"`
	Class      string `json:"class,omitempty"`
	Percentage int    `json:"percentage"`
}

// queryBatteryField sends `wireless.battery.<side>.<field>` and returns the
// integer the keyboard replies with. `field` is either "level" or "status".
func queryBatteryField(c *neuron.Client, side, field string) (int, error) {
	resp, err := c.Query("wireless.battery." + side + "." + field)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(resp)
}

// percentageForIcon picks a value for waybar's icon picker. The firmware
// returns placeholder levels for non-discharging sides (99 when charging,
// 100 when the side is unreachable), so we use only sides whose status is
// "discharging" for the icon. If no side has a real reading, fall back to
// 100 if any side is charging (semantically "full") or 0 otherwise
// (semantically "no battery info").
func percentageForIcon(b batteryLevel) int {
	leftReal := b.LeftStatus == statusDischarging
	rightReal := b.RightStatus == statusDischarging
	switch {
	case leftReal && rightReal:
		return min(b.Left, b.Right)
	case leftReal:
		return b.Left
	case rightReal:
		return b.Right
	case b.LeftStatus == statusCharging || b.RightStatus == statusCharging:
		return 100
	default:
		return 0
	}
}

func main() {
	dev, err := neuron.FindDev()
	if err != nil {
		log.Fatal("Could not find keyboard:", err)
	}
	client, err := neuron.Open(dev)
	if err != nil {
		log.Fatal("failed to open port:", err)
	}
	defer client.Close()

	battery := batteryLevel{}
	var exit int
	// Bail on the first failure: a timed-out reply may still arrive later
	// and would be consumed by the next query, desyncing every value after it.
queries:
	for _, side := range []struct {
		name   string
		level  *int
		status *sideStatus
	}{
		{"left", &battery.Left, &battery.LeftStatus},
		{"right", &battery.Right, &battery.RightStatus},
	} {
		v, err := queryBatteryField(client, side.name, "level")
		if err != nil {
			log.Printf("failed to get %s battery level: %v", side.name, err)
			exit = 1
			break queries
		}
		*side.level = v

		// When the side is charging the level is a 99 placeholder; when the
		// side is unreachable (RF off / out of range) the level is a 100
		// placeholder. Trust status, not level, to distinguish those states.
		s, err := queryBatteryField(client, side.name, "status")
		if err != nil {
			log.Printf("failed to get %s battery status: %v", side.name, err)
			exit = 1
			break queries
		}
		*side.status = sideStatus(s)
	}

	fmtSide := func(label string, level int, status sideStatus) string {
		switch status {
		case statusCharging:
			return label + ":CHG"
		case statusDisconnected:
			return label + ":OFF"
		default:
			return fmt.Sprintf("%s:%d%%", label, level)
		}
	}
	fmtTooltipSide := func(label string, level int, status sideStatus) string {
		switch status {
		case statusCharging:
			return label + " side: charging"
		case statusDisconnected:
			return label + " side: not connected"
		default:
			return fmt.Sprintf("%s side: %d%%", label, level)
		}
	}

	output := WaybarOutput{
		Text: fmtSide("L", battery.Left, battery.LeftStatus) + " " +
			fmtSide("R", battery.Right, battery.RightStatus),
		Tooltip: fmtTooltipSide("Left", battery.Left, battery.LeftStatus) + "\r" +
			fmtTooltipSide("Right", battery.Right, battery.RightStatus),
		Percentage: percentageForIcon(battery),
	}

	anyDisconnected := battery.LeftStatus == statusDisconnected || battery.RightStatus == statusDisconnected
	anyCharging := battery.LeftStatus == statusCharging || battery.RightStatus == statusCharging
	leftLow := battery.LeftStatus == statusDischarging && battery.Left < 20
	rightLow := battery.RightStatus == statusDischarging && battery.Right < 20
	switch {
	case anyDisconnected:
		output.Class = "disconnected"
	case anyCharging:
		output.Class = "charging"
	case leftLow || rightLow:
		output.Class = "critical"
	}

	jsonOutput, err := json.Marshal(output)
	if err != nil {
		log.Fatal("failed to marshal json:", err)
	}

	fmt.Println(string(jsonOutput))
	os.Exit(exit)
}
