//go:build unix && !linux

package fsbuiltins

import "testing"

type noFollowMode struct {
	name string
	set  func(t *testing.T)
}

// noFollowModes: only the component walk exists here.
func noFollowModes() []noFollowMode {
	return []noFollowMode{{"walk", func(*testing.T) {}}}
}

func mutateFastPath() (restore func()) { return func() {} }
