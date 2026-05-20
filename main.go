package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"dygma-indicator/internal/neuron"
)

// sideStatus mirrors `wireless.battery.<side>.status` returned by the firmware.
// 0 = discharging, 1 = charging, 4 = unreachable (RF off / out of range).
// Any other value is treated as "unknown" so we surface that explicitly
// instead of pretending the (possibly placeholder) level is a real reading.
type sideStatus int

const (
	statusDischarging  sideStatus = 0
	statusCharging     sideStatus = 1
	statusDisconnected sideStatus = 4
)

const lowBatteryThreshold = 20

// known reports whether s is one of the firmware status values we've observed
// and modelled. Anything else is treated as "unknown" by the renderers and
// promoted to the highest-precedence class so a future firmware sentinel
// can't silently masquerade as a real percentage.
func (s sideStatus) known() bool {
	switch s {
	case statusDischarging, statusCharging, statusDisconnected:
		return true
	}
	return false
}

// text returns the short per-side label used in waybar's `text` field.
func (s sideStatus) text(label string, level int) string {
	switch s {
	case statusCharging:
		return label + ":CHG"
	case statusDisconnected:
		return label + ":OFF"
	case statusDischarging:
		return fmt.Sprintf("%s:%d%%", label, level)
	default:
		return label + ":?"
	}
}

// tooltip returns the long per-side label used in waybar's `tooltip` field.
func (s sideStatus) tooltip(label string, level int) string {
	switch s {
	case statusCharging:
		return label + " side: charging"
	case statusDisconnected:
		return label + " side: not connected"
	case statusDischarging:
		return fmt.Sprintf("%s side: %d%%", label, level)
	default:
		return fmt.Sprintf("%s side: unknown status %d", label, int(s))
	}
}

type batteryLevel struct {
	Left, Right             int
	LeftStatus, RightStatus sideStatus
}

type waybarOutput struct {
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

// querySide fetches both the level and status for a single side. Returns on
// the first error so the caller doesn't consume a late reply as the response
// to a subsequent query.
func querySide(c *neuron.Client, side string) (level int, status sideStatus, err error) {
	level, err = queryBatteryField(c, side, "level")
	if err != nil {
		return 0, 0, fmt.Errorf("%s level: %w", side, err)
	}
	s, err := queryBatteryField(c, side, "status")
	if err != nil {
		return 0, 0, fmt.Errorf("%s status: %w", side, err)
	}
	return level, sideStatus(s), nil
}

// percentageForIcon picks a value for waybar's icon picker. The firmware
// returns placeholder levels for non-discharging sides (99 when charging,
// 100 when the side is unreachable), so we use only sides whose status is
// "discharging" for the icon. If no side has a real reading, fall back to
// 100 if any side is charging (semantically "full") or 0 otherwise
// (semantically "no battery info"). Unknown statuses are intentionally not
// treated as real readings here.
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

// emitErrorJSON writes a valid waybar JSON payload describing an error
// condition. Waybar shows the text + tooltip + class so the user sees
// something actionable instead of an empty/broken module.
func emitErrorJSON(err error) {
	out := waybarOutput{
		Text:    "?",
		Tooltip: err.Error(),
		Class:   "error",
	}
	b, _ := json.Marshal(out)
	fmt.Println(string(b))
}

func main() {
	if err := run(); err != nil {
		emitErrorJSON(err)
		os.Exit(1)
	}
}

func run() error {
	dev, err := neuron.FindDev()
	if err != nil {
		return fmt.Errorf("could not find keyboard: %w", err)
	}
	client, err := neuron.Open(dev)
	if err != nil {
		return fmt.Errorf("failed to open port: %w", err)
	}
	defer client.Close()

	battery := batteryLevel{}
	// Bail on the first failure: a timed-out reply may still arrive later
	// and would be consumed by the next query, desyncing every value after it.
	battery.Left, battery.LeftStatus, err = querySide(client, "left")
	if err != nil {
		return err
	}
	battery.Right, battery.RightStatus, err = querySide(client, "right")
	if err != nil {
		return err
	}

	output := waybarOutput{
		Text: battery.LeftStatus.text("L", battery.Left) + " " +
			battery.RightStatus.text("R", battery.Right),
		Tooltip: battery.LeftStatus.tooltip("Left", battery.Left) + "\n" +
			battery.RightStatus.tooltip("Right", battery.Right),
		Percentage: percentageForIcon(battery),
	}

	leftUnknown := !battery.LeftStatus.known()
	rightUnknown := !battery.RightStatus.known()
	anyDisconnected := battery.LeftStatus == statusDisconnected || battery.RightStatus == statusDisconnected
	anyCharging := battery.LeftStatus == statusCharging || battery.RightStatus == statusCharging
	leftLow := battery.LeftStatus == statusDischarging && battery.Left < lowBatteryThreshold
	rightLow := battery.RightStatus == statusDischarging && battery.Right < lowBatteryThreshold
	// Class precedence (highest first):
	//   unknown      — a status value we don't model; don't trust the rest.
	//   critical     — a real discharging side below the low threshold.
	//   disconnected — more informative than charging (which is transient).
	//   charging     — at least one side plugged in.
	switch {
	case leftUnknown || rightUnknown:
		output.Class = "unknown"
	case leftLow || rightLow:
		output.Class = "critical"
	case anyDisconnected:
		output.Class = "disconnected"
	case anyCharging:
		output.Class = "charging"
	}

	jsonOutput, err := json.Marshal(output)
	if err != nil {
		return fmt.Errorf("failed to marshal json: %w", err)
	}

	fmt.Println(string(jsonOutput))
	return nil
}
