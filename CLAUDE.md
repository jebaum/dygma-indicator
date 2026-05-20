# Dygma Indicator

A one-shot CLI that queries a Dygma keyboard's neuron (USB dongle) over serial, prints a `waybar`-shaped JSON line, and exits. One-shot because the neuron's serial port is single-user — holding it open blocks Bazecor and other clients (see README).

## Files

- `main.go` — query loop and output formatting.
- `find_dev_darwin.go` (Darwin) / `find_dev_generic.go` (`!darwin`) — neuron discovery by USB vendor ID (`1209`, `35ef`). Per the README, the Darwin path is unverified.
- `cmd/probe/main.go` — diagnostic tool that fires a list of commands and dumps raw + hex responses. Extend its `commands` slice when investigating a new firmware state.

## Serial protocol

- 9600 baud, line-oriented ASCII.
- Send `<command>\n`. The keyboard replies with the payload followed by a `\r\n.\r\n` terminator — the `.` line is the end-of-response marker.
- Unknown/unimplemented commands return an empty payload (just `\r\n.\r\n`).
- `help` returns a newline-separated catalogue of every supported command.

## Battery state semantics (load-bearing)

The firmware encodes non-discharging states by overloading the `level` field with sentinel values. **Trust `status`, not `level`, to distinguish them.**

| state | `wireless.battery.<side>.status` | `wireless.battery.<side>.level` |
| --- | --- | --- |
| discharging | `0` | real percentage (0–100) |
| charging via USB | `1` | `99` (placeholder) |
| unreachable — RF off / out of range | `4` | `100` (placeholder) |

The `sideStatus` enum and `percentageForIcon` in `main.go` exist to handle this. A disconnected side's fake `100` could silently mask a low-battery condition on the other side, so the `critical` class is gated on `statusDischarging`.

Status values outside this table have not been observed; the code falls through to "treat as discharging" so the (possibly real) level is at least surfaced.

## Query loop invariant

`main.go` bails on the first failed query (timeout or parse error). A late reply for an aborted command would otherwise be consumed as the response to the *next* command, desyncing every value after it. Don't change this without designing a resync.

## Firmware command catalogue

From `help` on firmware `v1.4.1` (Raise2). Read with `cmd/probe`.

Battery / wireless:
- `wireless.battery.{left,right}.{level,status}` — see table above.
- `wireless.battery.savingMode`
- `wireless.rf.{power,channelHop,syncPairing}`

Hardware diagnostics (useful when investigating connectivity):
- `hardware.version` — model name (e.g. `Raise2`).
- `hardware.firmware` / `version` — firmware version.
- `hardware.wireless` — `true` for wireless models.
- `hardware.side_power` — `0` when neither half is powered.
- `hardware.side_ver` — keyscanner versions; `0 0` when halves are unreachable.
- `hardware.chip_id` / `hardware.chip_id.{left,right,left_rf,right_rf}` — chip IDs. **Cached** — returned even when sides are offline, so not a reliable liveness signal on their own.
- `hardware.chip_info`

Out of scope for this tool but available: `keymap.*`, `led.*`, `superkeys.*`, `qukeys.*`, `macros.*`, `mouse.*`, `layer.*`, `palette`, `colormap.map`, `idleleds.*`, `settings.*`, `eeprom.*`, `upgrade.*`.

## Investigation workflow

When firmware behavior is unclear (e.g. a new sentinel value, an undocumented state):

1. Add the commands of interest to `cmd/probe/main.go`'s `commands` slice.
2. `go build -o bin/probe ./cmd/probe && bin/probe > some-state.txt` while the keyboard is in the state being investigated. `bin/` is git-ignored.
3. Compare the captured `raw` / `hex` outputs across states to find the discriminating field.

The `raw` and `hex` views matter together — trailing spaces and `\r` placement vary between commands and aren't visible in `raw` alone.
