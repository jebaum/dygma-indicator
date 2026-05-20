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
		name   string
		level  *int
		status *sideStatus
	}{
		{"left", &battery.Left, &battery.LeftStatus},
		{"right", &battery.Right, &battery.RightStatus},
	} {
		v, err := queryBatteryField(port, side.name, "level", ch, errCh)
		if err != nil {
			log.Printf("failed to get %s battery level: %v", side.name, err)
			exit = 1
			break queries
		}
		*side.level = v

		// When the side is charging the level is a 99 placeholder; when the
		// side is unreachable (RF off / out of range) the level is a 100
		// placeholder. Trust status, not level, to distinguish those states.
		s, err := queryBatteryField(port, side.name, "status", ch, errCh)
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
