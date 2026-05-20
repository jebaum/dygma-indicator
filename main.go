package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"

	"dygma-indicator/internal/neuron"
)

// version is overridable at build time via `-ldflags '-X main.version=...'`.
var version = "dev"

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

type sideReading struct {
	Level  int
	Status sideStatus
}

type batteryState struct {
	Left, Right sideReading
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
	n, err := strconv.Atoi(resp)
	if err != nil {
		return 0, fmt.Errorf("parse %s.%s response %q: %w", side, field, resp, err)
	}
	return n, nil
}

// querySide fetches both the level and status for a single side. Returns on
// the first error so the caller doesn't consume a late reply as the response
// to a subsequent query.
func querySide(c *neuron.Client, side string) (sideReading, error) {
	level, err := queryBatteryField(c, side, "level")
	if err != nil {
		return sideReading{}, fmt.Errorf("%s level: %w", side, err)
	}
	s, err := queryBatteryField(c, side, "status")
	if err != nil {
		return sideReading{}, fmt.Errorf("%s status: %w", side, err)
	}
	return sideReading{Level: level, Status: sideStatus(s)}, nil
}

// percentageForIcon picks a value for waybar's icon picker. The firmware
// returns placeholder levels for non-discharging sides (99 when charging,
// 100 when the side is unreachable), so we use only sides whose status is
// "discharging" for the icon. If no side has a real reading, fall back to
// 100 if any side is charging (semantically "full") or 0 otherwise
// (semantically "no battery info"). Unknown statuses are intentionally not
// treated as real readings here.
func percentageForIcon(b batteryState) int {
	leftReal := b.Left.Status == statusDischarging
	rightReal := b.Right.Status == statusDischarging
	switch {
	case leftReal && rightReal:
		return min(b.Left.Level, b.Right.Level)
	case leftReal:
		return b.Left.Level
	case rightReal:
		return b.Right.Level
	case b.Left.Status == statusCharging || b.Right.Status == statusCharging:
		return 100
	default:
		return 0
	}
}

// classify returns the waybar class for a battery state, following the
// precedence: unknown > critical > disconnected > charging > (empty).
func classify(b batteryState) string {
	leftUnknown := !b.Left.Status.known()
	rightUnknown := !b.Right.Status.known()
	anyDisconnected := b.Left.Status == statusDisconnected || b.Right.Status == statusDisconnected
	anyCharging := b.Left.Status == statusCharging || b.Right.Status == statusCharging
	leftLow := b.Left.Status == statusDischarging && b.Left.Level < lowBatteryThreshold
	rightLow := b.Right.Status == statusDischarging && b.Right.Level < lowBatteryThreshold
	switch {
	case leftUnknown || rightUnknown:
		return "unknown"
	case leftLow || rightLow:
		return "critical"
	case anyDisconnected:
		return "disconnected"
	case anyCharging:
		return "charging"
	}
	return ""
}

// render builds the waybar output for a battery state. Pure: no I/O.
func render(b batteryState) waybarOutput {
	return waybarOutput{
		Text: b.Left.Status.text("L", b.Left.Level) + " " +
			b.Right.Status.text("R", b.Right.Level),
		Tooltip: b.Left.Status.tooltip("Left", b.Left.Level) + "\n" +
			b.Right.Status.tooltip("Right", b.Right.Level),
		Class:      classify(b),
		Percentage: percentageForIcon(b),
	}
}

// emit writes a waybar JSON line to stdout. Used by both success and error paths.
func emit(out waybarOutput) {
	b, err := json.Marshal(out)
	if err != nil {
		// waybarOutput only has string/int fields, so this is unreachable
		// in practice. Fall back to a hand-rolled JSON string so the module
		// never goes silent.
		fmt.Println(`{"text":"?","class":"error","tooltip":"json marshal failed"}`)
		return
	}
	fmt.Println(string(b))
}

// emitErrorJSON writes a valid waybar JSON payload describing an error
// condition. Waybar shows the text + tooltip + class so the user sees
// something actionable instead of an empty/broken module.
func emitErrorJSON(err error) {
	emit(waybarOutput{
		Text:    "?",
		Tooltip: err.Error(),
		Class:   "error",
	})
}

func main() {
	deviceFlag := flag.String("device", "", "Serial device path (default: auto-detect)")
	debugFlag := flag.Bool("debug", false, "Log serial traffic to stderr")
	versionFlag := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Printf("dygma-indicator %s\n", version)
		return
	}

	if err := run(*deviceFlag, *debugFlag); err != nil {
		emitErrorJSON(err)
		os.Exit(1)
	}
}

func run(device string, debug bool) error {
	dev := device
	if dev == "" {
		var err error
		dev, err = neuron.FindDev()
		if err != nil {
			if errors.Is(err, neuron.ErrNotFound) {
				return fmt.Errorf("no Dygma keyboard detected (is the neuron plugged in?): %w", err)
			}
			return fmt.Errorf("could not find keyboard: %w", err)
		}
	}
	client, err := neuron.Open(dev)
	if err != nil {
		return fmt.Errorf("failed to open port: %w", err)
	}
	defer client.Close()

	if debug {
		client.SetDebug(os.Stderr)
	}

	state, err := queryBatteryState(client)
	if err != nil {
		return err
	}
	emit(render(state))
	return nil
}

func queryBatteryState(c *neuron.Client) (batteryState, error) {
	// Bail on the first failure: a timed-out reply may still arrive later
	// and would be consumed by the next query, desyncing every value after it.
	left, err := querySide(c, "left")
	if err != nil {
		return batteryState{}, err
	}
	right, err := querySide(c, "right")
	if err != nil {
		return batteryState{}, err
	}
	return batteryState{Left: left, Right: right}, nil
}
