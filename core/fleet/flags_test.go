package fleet

import (
	"testing"
)

func TestDisabled(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want bool
	}{
		{"unset (empty)", "", false},
		{"1", "1", true},
		{"true", "true", true},
		{"yes", "yes", true},
		{"TRUE", "TRUE", true},
		{"YES", "YES", true},
		{"0", "0", false},
		{"false", "false", false},
		{"no", "no", false},
		{"FALSE", "FALSE", false},
		{"NO", "NO", false},
		// Any other non-empty value is truthy (operator-friendly).
		{"disabled", "disabled", true},
		{"off", "off", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HARNESS_FLEET_DISABLED", tt.env)
			got := Disabled()
			if got != tt.want {
				t.Errorf("Disabled() = %v, want %v (env=%q)", got, tt.want, tt.env)
			}
		})
	}
}

func TestNopClientOnDisabled(t *testing.T) {
	t.Setenv("HARNESS_FLEET_DISABLED", "1")
	c, err := NewClient(ClientOpts{})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	// SignIn should return ErrFleetDisabled.
	_, err = c.SignIn(t.Context())
	if err != ErrFleetDisabled {
		t.Errorf("SignIn: got %v, want ErrFleetDisabled", err)
	}
}
