package fleet

// device_name.go — the device display name sent as the enroll `hostname`
// field (fleet contract §16; Fleet ER-43). Fleet's Hosts page labels it
// "Device name"; without it a harness install shows only its node id.
//
// Parity with the Fleet plugin relay (kameas-fleet-plugin internal/device,
// legal D58): the name is member-controlled. By default it is a neutral
// "<OS> device <4 hex>" label derived from the node id, the computer's own
// name is sent only when the member chooses it, a custom name overrides
// both, and the member can turn sharing off entirely (nothing is sent, and
// because Fleet's enroll upsert rewrites the column, the stored name is
// cleared on the next enroll).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// MaxDeviceNameLen is Fleet's bound on the enroll `hostname` (device name)
// field, in runes (kenaz-fleet service/enroll_client_kind.go
// maxDeviceNameLen; the relay's fleetapi.MaxDisplayNameLen).
const MaxDeviceNameLen = 64

// DeviceNameChoice is the member's device-name setting.
type DeviceNameChoice struct {
	// Share: send a device name at all. Off = the field is omitted.
	Share bool
	// UseComputerName: send this computer's own name instead of the
	// neutral default.
	UseComputerName bool
	// Custom, when non-empty after sanitising, overrides both.
	Custom string
}

// DefaultDeviceName returns "<OS> device <4 hex>", where the 4 hex
// characters are the first two bytes of SHA-256(nodeID) — the same label
// the relay uses. The node id itself is not revealed. Empty when nodeID is.
func DefaultDeviceName(goos, nodeID string) string {
	if strings.TrimSpace(nodeID) == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(nodeID))
	return deviceOSLabel(goos) + " device " + hex.EncodeToString(sum[:2])
}

func deviceOSLabel(goos string) string {
	switch goos {
	case "darwin":
		return "macOS"
	case "linux":
		return "Linux"
	case "windows":
		return "Windows"
	}
	return "Unknown OS"
}

// SanitizeDeviceName makes s acceptable to Fleet's validDeviceName: valid
// UTF-8, no control characters and no invisible format / line / paragraph
// separators (bidi overrides, zero-width characters), runs of whitespace
// collapsed, trimmed, and at most MaxDeviceNameLen runes. "" means nothing
// usable is left.
func SanitizeDeviceName(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsControl(r):
			return ' '
		case unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp):
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > MaxDeviceNameLen {
		s = strings.TrimSpace(string([]rune(s)[:MaxDeviceNameLen]))
	}
	return s
}

// ResolveDeviceName returns the name enroll sends for choice: "" when
// sharing is off; else the custom name, else (when chosen) the computer's
// name, else the neutral default. computerName is called only when the
// computer's name is actually needed.
func ResolveDeviceName(choice DeviceNameChoice, goos, nodeID string, computerName func() string) string {
	if !choice.Share {
		return ""
	}
	if n := SanitizeDeviceName(choice.Custom); n != "" {
		return n
	}
	if choice.UseComputerName && computerName != nil {
		if n := SanitizeDeviceName(computerName()); n != "" {
			return n
		}
	}
	return SanitizeDeviceName(DefaultDeviceName(goos, nodeID))
}

// ComputerName returns this computer's user-facing name, sanitised: on
// macOS the Sharing "Computer Name" (scutil --get ComputerName), on Windows
// %COMPUTERNAME%, elsewhere the hostname. Falls back to the short hostname.
// "" when none can be read. It is read only on demand (never cached), so a
// rename shows up on the next enroll.
func ComputerName() string {
	return computerNameFor(runtime.GOOS, scutilComputerName, os.Getenv, os.Hostname)
}

// computerNameFor is the pure core of ComputerName.
func computerNameFor(goos string, scutil func() (string, error), getenv func(string) string, hostname func() (string, error)) string {
	switch goos {
	case "darwin":
		if scutil != nil {
			if out, err := scutil(); err == nil {
				if n := SanitizeDeviceName(out); n != "" {
					return n
				}
			}
		}
	case "windows":
		if n := SanitizeDeviceName(getenv("COMPUTERNAME")); n != "" {
			return n
		}
	}
	if hostname == nil {
		return ""
	}
	h, err := hostname()
	if err != nil {
		return ""
	}
	// A fully qualified name ("box.corp.example.com", "Foo-MacBook.local")
	// is not a display name: keep the first label.
	if i := strings.IndexByte(h, '.'); i > 0 {
		h = h[:i]
	}
	return SanitizeDeviceName(h)
}

// scutilComputerName reads the macOS Sharing "Computer Name".
func scutilComputerName() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "scutil", "--get", "ComputerName").Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// SetDeviceNameSource registers fn, which returns the device name enroll
// sends for nodeID ("" = omit the field). The settings view wires it to
// the member's setting. With no source, enroll sends no name (the
// pre-ER-43 body). fn runs on the enroll goroutine; its result is
// sanitised again before it leaves the device.
func (c *Client) SetDeviceNameSource(fn func(nodeID string) string) {
	if c == nil || c.isNop {
		return
	}
	c.deviceName.Store(&fn)
}

// deviceNameFor returns the sanitised device name for this enroll.
func (c *Client) deviceNameFor(nodeID string) string {
	if c == nil {
		return ""
	}
	p := c.deviceName.Load()
	if p == nil || *p == nil {
		return ""
	}
	return SanitizeDeviceName((*p)(nodeID))
}
