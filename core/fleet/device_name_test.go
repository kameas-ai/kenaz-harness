package fleet

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

// Fake names only: no test reads this machine's real name.

func TestSanitizeDeviceName(t *testing.T) {
	cases := map[string]string{
		"":                              "",
		"   ":                           "",
		"  Test Laptop  ":               "Test Laptop",
		"Test\tLaptop\n":                "Test Laptop",
		"Test\x00Lap\x1btop":            "Test Lap top",
		"Test\u202eLaptop":              "TestLaptop", // bidi override dropped
		"Test\u200bLap\ufefftop":        "TestLaptop", // zero-width chars dropped
		"Line\u2028Sep\u2029Para":       "LineSepPara",
		"Example   Workstation\r\n 01 ": "Example Workstation 01",
		"Bad\xffUTF8":                   "BadUTF8",
		"\x7f":                          "",
	}
	for in, want := range cases {
		if got := SanitizeDeviceName(in); got != want {
			t.Errorf("SanitizeDeviceName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeDeviceName_LengthCap(t *testing.T) {
	long := strings.Repeat("é", MaxDeviceNameLen+10) // multi-byte runes
	got := SanitizeDeviceName(long)
	if n := utf8.RuneCountInString(got); n != MaxDeviceNameLen {
		t.Fatalf("rune count = %d, want %d", n, MaxDeviceNameLen)
	}
	if !utf8.ValidString(got) {
		t.Fatal("cap split a rune")
	}
	// A space at the cut is trimmed, never sent trailing.
	got = SanitizeDeviceName(strings.Repeat("a", MaxDeviceNameLen-1) + " tail")
	if strings.HasSuffix(got, " ") || utf8.RuneCountInString(got) != MaxDeviceNameLen-1 {
		t.Fatalf("got %q", got)
	}
	if MaxDeviceNameLen != 64 {
		t.Fatalf("MaxDeviceNameLen = %d; Fleet's maxDeviceNameLen is 64", MaxDeviceNameLen)
	}
}

func TestDefaultDeviceName(t *testing.T) {
	// SHA-256("01TESTNODE") — same derivation as the relay
	// (kameas-fleet-plugin internal/device.DefaultDisplayName).
	a := DefaultDeviceName("darwin", "01TESTNODE")
	if !strings.HasPrefix(a, "macOS device ") || len(a) != len("macOS device ")+4 {
		t.Fatalf("DefaultDeviceName = %q", a)
	}
	if a != DefaultDeviceName("darwin", "01TESTNODE") {
		t.Fatal("not deterministic")
	}
	if DefaultDeviceName("darwin", "01OTHERNODE") == a {
		t.Fatal("different nodes should (almost always) differ")
	}
	if strings.Contains(a, "01TESTNODE") {
		t.Fatal("the node id itself must not appear")
	}
	for goos, label := range map[string]string{"linux": "Linux", "windows": "Windows", "plan9": "Unknown OS"} {
		if got := DefaultDeviceName(goos, "n"); !strings.HasPrefix(got, label+" device ") {
			t.Errorf("%s: %q", goos, got)
		}
	}
	if got := DefaultDeviceName("darwin", " "); got != "" {
		t.Fatalf("empty node id: %q", got)
	}
}

func TestResolveDeviceName(t *testing.T) {
	calls := 0
	computer := func() string { calls++; return "Example-Workstation" }
	def := DefaultDeviceName("linux", "node-1")

	// Sharing off: nothing, and the computer's name is never read.
	if got := ResolveDeviceName(DeviceNameChoice{Share: false, UseComputerName: true, Custom: "Mine"}, "linux", "node-1", computer); got != "" {
		t.Fatalf("off: %q", got)
	}
	// Default: the neutral name, computer name not read.
	if got := ResolveDeviceName(DeviceNameChoice{Share: true}, "linux", "node-1", computer); got != def {
		t.Fatalf("default: %q, want %q", got, def)
	}
	if calls != 0 {
		t.Fatalf("computer name read %d times without being chosen", calls)
	}
	// Computer name chosen.
	if got := ResolveDeviceName(DeviceNameChoice{Share: true, UseComputerName: true}, "linux", "node-1", computer); got != "Example-Workstation" {
		t.Fatalf("computer: %q", got)
	}
	// Custom overrides the computer name, and is sanitised.
	if got := ResolveDeviceName(DeviceNameChoice{Share: true, UseComputerName: true, Custom: " Test\tLaptop\u200b "}, "linux", "node-1", computer); got != "Test Laptop" {
		t.Fatalf("custom: %q", got)
	}
	// A custom name that sanitises to nothing falls through.
	if got := ResolveDeviceName(DeviceNameChoice{Share: true, Custom: "\x00\u200b "}, "linux", "node-1", computer); got != def {
		t.Fatalf("empty custom: %q", got)
	}
	// Computer name unreadable: the neutral default, not nothing.
	if got := ResolveDeviceName(DeviceNameChoice{Share: true, UseComputerName: true}, "linux", "node-1", func() string { return "" }); got != def {
		t.Fatalf("unreadable computer name: %q", got)
	}
	// No node id and nothing else: empty (omitted).
	if got := ResolveDeviceName(DeviceNameChoice{Share: true}, "linux", "", computer); got != "" {
		t.Fatalf("no node id: %q", got)
	}
}

func TestComputerNameFor(t *testing.T) {
	env := envOf(map[string]string{"COMPUTERNAME": "TEST-PC"})
	host := func() (string, error) { return "test-host.corp.example", nil }
	scutil := func() (string, error) { return "Test’s MacBook\n", nil }
	failScutil := func() (string, error) { return "", errors.New("no scutil") }
	failHost := func() (string, error) { return "", errors.New("no hostname") }

	if got := computerNameFor("darwin", scutil, env, host); got != "Test’s MacBook" {
		t.Fatalf("darwin: %q", got)
	}
	if got := computerNameFor("darwin", failScutil, env, host); got != "test-host" {
		t.Fatalf("darwin fallback: %q", got)
	}
	if got := computerNameFor("windows", nil, env, host); got != "TEST-PC" {
		t.Fatalf("windows: %q", got)
	}
	if got := computerNameFor("windows", nil, envOf(nil), host); got != "test-host" {
		t.Fatalf("windows fallback: %q", got)
	}
	if got := computerNameFor("linux", scutil, env, host); got != "test-host" {
		t.Fatalf("linux: %q", got)
	}
	if got := computerNameFor("linux", nil, env, failHost); got != "" {
		t.Fatalf("linux no hostname: %q", got)
	}
}
