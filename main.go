package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"go.bug.st/serial"
)

var vendorIds = []string{"1209", "35ef"}

type batteryLevel struct {
	Left          int  `json:"left"`
	Right         int  `json:"right"`
	LeftCharging  bool `json:"leftCharging"`
	RightCharging bool `json:"rightCharging"`
}

type WaybarOutput struct {
	Text       string `json:"text"`
	Tooltip    string `json:"tooltip"`
	Class      string `json:"class,omitempty"`
	Percentage int    `json:"percentage"`
}

func readFromPort(ctx context.Context, port serial.Port, ch chan<- int, errCh chan<- error) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			buff := make([]byte, 4)
			n, err := port.Read(buff)
			if err != nil {
				if err.Error() != "EOF" {
					errCh <- fmt.Errorf("error reading from port: %w", err)
					return
				}
				continue
			}

			if n > 0 {
				response := strings.TrimSuffix(string(bytes.TrimSpace(buff[:n])), ".")
				if response == "" {
					continue
				}
				v, err := strconv.Atoi(response)
				if err != nil {
					errCh <- fmt.Errorf("failed to parse %q", response)
					return
				}
				ch <- v
			}
		}
	}
}

const queryTimeout = 2 * time.Second

// queryBatteryField sends `wireless.battery.<side>.<field>` and returns the
// integer the keyboard replies with. `field` is either "level" or "status".
func queryBatteryField(port serial.Port, side, field string, ch <-chan int, errCh <-chan error) (int, error) {
	command := "wireless.battery." + side + "." + field + "\n"
	if _, err := port.Write([]byte(command)); err != nil {
		return 0, fmt.Errorf("failed to send command to keyboard: %w", err)
	}

	select {
	case v := <-ch:
		return v, nil
	case err := <-errCh:
		return 0, err
	case <-time.After(queryTimeout):
		return 0, fmt.Errorf("timed out waiting for %s.%s response", side, field)
	}
}

// chargingAwarePercentage returns a value suitable for waybar's icon picker:
// the lowest level among sides that are NOT charging, since charging sides
// report a 99 placeholder rather than a real reading. If both sides are
// charging, returns 100 (rather than the firmware's 99) so the icon picker
// lands on the "full" bucket — semantically "plugged in, treat as full".
func chargingAwarePercentage(b batteryLevel) int {
	switch {
	case b.LeftCharging && b.RightCharging:
		return 100
	case b.LeftCharging:
		return b.Right
	case b.RightCharging:
		return b.Left
	default:
		return min(b.Left, b.Right)
	}
}

func main() {
	dev, err := findKeyboardDev()
	if err != nil {
		log.Fatal("Could not find keyboard:", err)
	}
	mode := &serial.Mode{BaudRate: 9600}
	port, err := serial.Open(dev, mode)
	if err != nil {
		log.Fatal("failed to open port:", err)
	}
	defer port.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := make(chan int)
	errCh := make(chan error, 1)
	go readFromPort(ctx, port, ch, errCh)

	battery := batteryLevel{}
	var exit int
	// Bail on the first failure: a timed-out reply may still arrive later
	// and would be consumed by the next query, desyncing every value after it.
queries:
	for _, side := range []struct {
		name     string
		level    *int
		charging *bool
	}{
		{"left", &battery.Left, &battery.LeftCharging},
		{"right", &battery.Right, &battery.RightCharging},
	} {
		v, err := queryBatteryField(port, side.name, "level", ch, errCh)
		if err != nil {
			log.Printf("failed to get %s battery level: %v", side.name, err)
			exit = 1
			break queries
		}
		*side.level = v

		// status: 0 = discharging, 1 = charging. When charging, the level
		// command returns a fixed 99 placeholder, so trust status here.
		s, err := queryBatteryField(port, side.name, "status", ch, errCh)
		if err != nil {
			log.Printf("failed to get %s battery status: %v", side.name, err)
			exit = 1
			break queries
		}
		*side.charging = s == 1
	}

	fmtSide := func(label string, level int, charging bool) string {
		if charging {
			return label + ":CHG"
		}
		return fmt.Sprintf("%s:%d%%", label, level)
	}
	fmtTooltipSide := func(label string, level int, charging bool) string {
		if charging {
			return label + " side: charging"
		}
		return fmt.Sprintf("%s side: %d%%", label, level)
	}

	output := WaybarOutput{
		Text: fmtSide("L", battery.Left, battery.LeftCharging) + " " +
			fmtSide("R", battery.Right, battery.RightCharging),
		Tooltip: fmtTooltipSide("Left", battery.Left, battery.LeftCharging) + "\r" +
			fmtTooltipSide("Right", battery.Right, battery.RightCharging),
		Percentage: chargingAwarePercentage(battery),
	}

	switch {
	case battery.LeftCharging || battery.RightCharging:
		output.Class = "charging"
	case battery.Left < 20 || battery.Right < 20:
		output.Class = "critical"
	}

	jsonOutput, err := json.Marshal(output)
	if err != nil {
		log.Fatal("failed to marshal json:", err)
	}

	fmt.Println(string(jsonOutput))
	os.Exit(exit)
}
