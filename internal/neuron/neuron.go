package neuron

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"go.bug.st/serial"
	"go.bug.st/serial/enumerator"
)

// knownDevices lists USB vendor/product IDs that identify a Dygma neuron.
// An empty PID means "match any product for this VID". This is intentionally
// liberal because we don't have a confirmed device list; tighten later as
// PIDs are confirmed.
//
// TODO: confirm and pin PIDs. Known so far: VID 1209 PID 2201 is the original
// Raise on the pid.codes shared VID; VID 35ef is Dygma's own assigned vendor
// block (Defy, Raise2, etc.).
var knownDevices = []struct{ VID, PID string }{
	{"35ef", ""},
	{"1209", ""},
}

const (
	// readTimeout bounds a single port.Read() call; queryTotal bounds the whole response.
	readTimeout = 200 * time.Millisecond
	queryTotal  = 2 * time.Second
)

const baudRate = 9600 // fixed by the Dygma firmware

// terminator is the firmware's end-of-response marker.
var terminator = []byte("\r\n.\r\n")

// ErrNotFound is returned by FindDev when no Dygma device is attached.
var ErrNotFound = errors.New("dygma keyboard not found")

// FindDev returns the serial-port path for an attached Dygma keyboard.
// Uses go.bug.st/serial/enumerator's USB metadata on all platforms
// (Linux, Darwin, Windows).
func FindDev() (string, error) {
	ports, err := enumerator.GetDetailedPortsList()
	if err != nil {
		return "", fmt.Errorf("enumerate serial ports: %w", err)
	}
	for _, port := range ports {
		for _, known := range knownDevices {
			if !strings.EqualFold(port.VID, known.VID) {
				continue
			}
			if known.PID != "" && !strings.EqualFold(port.PID, known.PID) {
				continue
			}
			return port.Name, nil
		}
	}
	return "", ErrNotFound
}

// transport is the subset of serial.Port that Query needs.
type transport interface {
	io.ReadWriter
}

// Client wraps a transport and owns the firmware's line-oriented framing.
type Client struct {
	rw    transport
	close func() error
	debug io.Writer
}

// Open opens the neuron at dev at 9600 baud and returns a Client.
// The caller must Close() it.
// While the port is open, no other client (Bazecor, another indicator instance)
// can talk to the keyboard, so callers should Close() as soon as possible.
func Open(dev string) (*Client, error) {
	port, err := serial.Open(dev, &serial.Mode{BaudRate: baudRate})
	if err != nil {
		return nil, err
	}
	if err := port.SetReadTimeout(readTimeout); err != nil {
		_ = port.Close()
		return nil, err
	}
	if err := port.ResetInputBuffer(); err != nil {
		_ = port.Close()
		return nil, fmt.Errorf("drain port: %w", err)
	}
	return &Client{rw: port, close: port.Close}, nil
}

// newClient builds a Client around an arbitrary transport. Used by tests to
// drive the framing logic with a scripted fake.
func newClient(rw transport) *Client {
	return &Client{rw: rw, close: func() error { return nil }}
}

// Close closes the underlying port.
func (c *Client) Close() error {
	return c.close()
}

// SetDebug enables stderr logging of every write and read for
// troubleshooting (used by main's --debug flag).
func (c *Client) SetDebug(w io.Writer) {
	c.debug = w
}

// Query sends `cmd\n` and reads the response payload (everything before
// the `\r\n.\r\n` terminator). Returns the payload as a trimmed string.
// Returns an error if the read times out before the terminator arrives,
// if the underlying read fails, or if the terminator is malformed.
// On timeout or read error, the caller must not issue further queries on
// this Client — a late reply could be consumed as the next query's response.
// See CLAUDE.md.
func (c *Client) Query(cmd string) (string, error) {
	if c.debug != nil {
		fmt.Fprintf(c.debug, "> %s\n", cmd)
	}
	if _, err := c.rw.Write([]byte(cmd + "\n")); err != nil {
		return "", fmt.Errorf("write %q: %w", cmd, err)
	}

	deadline := time.Now().Add(queryTotal)
	var acc []byte
	buf := make([]byte, 256)
	for {
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timed out waiting for response to %q", cmd)
		}
		n, err := c.rw.Read(buf)
		if err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("read after %q: %w", cmd, err)
		}
		if n > 0 {
			chunk := buf[:n]
			if c.debug != nil {
				fmt.Fprintf(c.debug, "< %q\n", string(chunk))
			}
			acc = append(acc, chunk...)
			if idx := bytes.Index(acc, terminator); idx >= 0 {
				return string(bytes.TrimSpace(acc[:idx])), nil
			}
		}
	}
}
