package settings

// fleet_device_name.go — Settings › Account › Device name (Fleet ER-43):
// the device display name the enroll body carries as `hostname`, which
// Fleet's Hosts page shows admins as "Device name".
//
// The choice is the member's (relay parity, legal D58): share or not
// (default share), and when sharing, the neutral "<OS> device <4 hex>"
// default, this computer's own name, or a custom name. The fields are
// written only by SetFleetDeviceName; a whole-settings Set keeps them.

import (
	"context"
	"runtime"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// computerNameFn reads this computer's name; tests replace it so a real
// machine name never appears in test output.
var computerNameFn = fleet.ComputerName

// deviceNameReenrollTimeout bounds the background re-enroll after a change.
const deviceNameReenrollTimeout = 60 * time.Second

func (s Settings) fleetDeviceNameChoice() fleet.DeviceNameChoice {
	return fleet.DeviceNameChoice{
		Share:           !s.FleetDeviceNameHidden,
		UseComputerName: s.FleetDeviceNameUseComputerName,
		Custom:          s.FleetDeviceNameCustom,
	}
}

// fleetDeviceName is the fleet.Client device-name source: the name the
// enroll for nodeID sends, read fresh from the store on every enroll. A
// store read error sends nothing — the member may have turned sharing off,
// and an unknown setting must not publish a name.
func (a *API) fleetDeviceName(nodeID string) string {
	if a == nil || a.store == nil {
		return ""
	}
	s, err := a.store.LoadAll()
	if err != nil {
		logging.L().Warn("fleet.device_name.settings_unreadable", "err", err.Error())
		return ""
	}
	return fleet.ResolveDeviceName(s.fleetDeviceNameChoice(), runtime.GOOS, nodeID, computerNameFn)
}

// GetFleetDeviceName returns the device-name setting with the read-only
// computer, default and effective names filled.
func (a *API) GetFleetDeviceName(_ context.Context) (FleetDeviceNameSettings, error) {
	s, err := a.store.LoadAll()
	if err != nil {
		return FleetDeviceNameSettings{}, err
	}
	nodeID := fleet.ReadNodeID(a.fleetDataDir())
	computer := computerNameFn()
	choice := s.fleetDeviceNameChoice()
	return FleetDeviceNameSettings{
		Share:           choice.Share,
		UseComputerName: choice.UseComputerName,
		CustomName:      s.FleetDeviceNameCustom,
		ComputerName:    computer,
		DefaultName:     fleet.DefaultDeviceName(runtime.GOOS, nodeID),
		EffectiveName: fleet.ResolveDeviceName(choice, runtime.GOOS, nodeID,
			func() string { return computer }),
		MaxLength: fleet.MaxDeviceNameLen,
	}, nil
}

// SetFleetDeviceName persists the device-name setting (the read-only fields
// are ignored; the custom name is stored sanitised) and, when this install
// is enrolled, re-enrolls in the background so Fleet shows the new name —
// or clears it, when sharing was turned off — without waiting for the next
// start. Fleet's enroll throttle keys on the body, so a changed name is
// always written.
func (a *API) SetFleetDeviceName(_ context.Context, dn FleetDeviceNameSettings) error {
	if err := Update(a.store, func(s *Settings) error {
		s.FleetDeviceNameHidden = !dn.Share
		s.FleetDeviceNameUseComputerName = dn.UseComputerName
		s.FleetDeviceNameCustom = fleet.SanitizeDeviceName(dn.CustomName)
		return nil
	}); err != nil {
		return err
	}
	a.reenrollForDeviceName()
	return nil
}

// reenrollForDeviceName pushes a changed device name to Fleet when this
// install is enrolled. Best-effort: a failure is logged, and every app
// start enrolls anyway.
func (a *API) reenrollForDeviceName() {
	if a.fleet == nil || a.fleetClient() == nil || fleet.Disabled() {
		return
	}
	a.fleet.mu.RLock()
	enrolled := a.fleet.enrolled
	a.fleet.mu.RUnlock()
	if !enrolled {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), deviceNameReenrollTimeout)
		defer cancel()
		if _, err := a.FleetRefreshIdentity(ctx); err != nil {
			logging.L().Warn("fleet.device_name.reenroll_failed", "err", err.Error())
		}
	}()
}
