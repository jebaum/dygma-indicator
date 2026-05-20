package main

import (
	"strings"
	"testing"
)

func reading(level int, status sideStatus) sideReading {
	return sideReading{Level: level, Status: status}
}

func TestPercentageForIcon(t *testing.T) {
	tests := []struct {
		name string
		in   batteryState
		want int
	}{
		{
			name: "both discharging returns min",
			in:   batteryState{Left: reading(80, statusDischarging), Right: reading(40, statusDischarging)},
			want: 40,
		},
		{
			name: "left discharging right charging sentinel 99 returns left",
			in:   batteryState{Left: reading(40, statusDischarging), Right: reading(99, statusCharging)},
			want: 40,
		},
		{
			name: "left discharging right disconnected sentinel 100 returns left (sentinel must not mask)",
			in:   batteryState{Left: reading(60, statusDischarging), Right: reading(100, statusDisconnected)},
			want: 60,
		},
		{
			name: "both charging returns 100",
			in:   batteryState{Left: reading(99, statusCharging), Right: reading(99, statusCharging)},
			want: 100,
		},
		{
			name: "both disconnected returns 0",
			in:   batteryState{Left: reading(100, statusDisconnected), Right: reading(100, statusDisconnected)},
			want: 0,
		},
		{
			name: "unknown one side, other discharging returns discharging side",
			in:   batteryState{Left: reading(55, statusDischarging), Right: reading(0, sideStatus(7))},
			want: 55,
		},
		{
			name: "both unknown returns 0",
			in:   batteryState{Left: reading(0, sideStatus(9)), Right: reading(0, sideStatus(7))},
			want: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := percentageForIcon(tc.in)
			if got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		name string
		in   batteryState
		want string
	}{
		{
			name: "both discharging at 50% — no class",
			in:   batteryState{Left: reading(50, statusDischarging), Right: reading(50, statusDischarging)},
			want: "",
		},
		{
			name: "left 10% discharging, right 50% discharging — critical",
			in:   batteryState{Left: reading(10, statusDischarging), Right: reading(50, statusDischarging)},
			want: "critical",
		},
		{
			name: "left 10% but charging sentinel — not critical, charging",
			in:   batteryState{Left: reading(10, statusCharging), Right: reading(50, statusDischarging)},
			want: "charging",
		},
		{
			name: "one disconnected, other charging — disconnected wins over charging",
			in:   batteryState{Left: reading(100, statusDisconnected), Right: reading(99, statusCharging)},
			want: "disconnected",
		},
		{
			name: "unknown status trumps everything",
			in:   batteryState{Left: reading(10, statusDischarging), Right: reading(0, sideStatus(99))},
			want: "unknown",
		},
		{
			name: "both discharging at 25% — above threshold, no class",
			in:   batteryState{Left: reading(25, statusDischarging), Right: reading(25, statusDischarging)},
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classify(tc.in)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRender(t *testing.T) {
	tests := []struct {
		name           string
		in             batteryState
		wantClass      string
		wantPercentage int
		wantTextHas    []string
		wantTooltipHas []string
	}{
		{
			name:           "both discharging at 50%",
			in:             batteryState{Left: reading(50, statusDischarging), Right: reading(50, statusDischarging)},
			wantClass:      "",
			wantPercentage: 50,
			wantTextHas:    []string{"L:50%", "R:50%"},
			wantTooltipHas: []string{"Left side: 50%", "Right side: 50%"},
		},
		{
			name:           "left discharging, right charging",
			in:             batteryState{Left: reading(40, statusDischarging), Right: reading(99, statusCharging)},
			wantClass:      "charging",
			wantPercentage: 40,
			wantTextHas:    []string{"L:40%", "R:CHG"},
			wantTooltipHas: []string{"Left side: 40%", "Right side: charging"},
		},
		{
			name:           "left low, both discharging",
			in:             batteryState{Left: reading(10, statusDischarging), Right: reading(50, statusDischarging)},
			wantClass:      "critical",
			wantPercentage: 10,
			wantTextHas:    []string{"L:10%", "R:50%"},
			wantTooltipHas: []string{"Left side: 10%", "Right side: 50%"},
		},
		{
			name:           "right disconnected",
			in:             batteryState{Left: reading(75, statusDischarging), Right: reading(100, statusDisconnected)},
			wantClass:      "disconnected",
			wantPercentage: 75,
			wantTextHas:    []string{"L:75%", "R:OFF"},
			wantTooltipHas: []string{"Left side: 75%", "Right side: not connected"},
		},
		{
			name:           "unknown status surfaces",
			in:             batteryState{Left: reading(50, statusDischarging), Right: reading(0, sideStatus(42))},
			wantClass:      "unknown",
			wantPercentage: 50,
			wantTextHas:    []string{"L:50%", "R:?"},
			wantTooltipHas: []string{"Left side: 50%", "Right side: unknown status 42"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := render(tc.in)
			if out.Class != tc.wantClass {
				t.Errorf("class = %q, want %q", out.Class, tc.wantClass)
			}
			if out.Percentage != tc.wantPercentage {
				t.Errorf("percentage = %d, want %d", out.Percentage, tc.wantPercentage)
			}
			for _, want := range tc.wantTextHas {
				if !strings.Contains(out.Text, want) {
					t.Errorf("text %q missing %q", out.Text, want)
				}
			}
			for _, want := range tc.wantTooltipHas {
				if !strings.Contains(out.Tooltip, want) {
					t.Errorf("tooltip %q missing %q", out.Tooltip, want)
				}
			}
		})
	}
}
