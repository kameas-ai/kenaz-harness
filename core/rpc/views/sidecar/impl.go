package sidecar

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/mlsidecar"
)

// ErrUnavailable is returned by Enable/Update when the install action is
// not offered (unsupported platform, or no engine release published yet).
// Its message is the user-facing reason.
var ErrUnavailable = errors.New("local recommendations are not available")

// ErrNotInstalled is returned by Update/Repair/Uninstall-adjacent calls
// that need an installed engine.
var ErrNotInstalled = errors.New("the ML engine is not installed")

// Impl implements SidecarAPI over a *mlsidecar.Manager.
type Impl struct {
	// Manager is the process's single sidecar manager. nil (nil-core test
	// chassis, or no data dir) makes every call return an honest
	// unavailable status rather than panicking.
	Manager *mlsidecar.Manager
	// Release resolves the harness's engine pin. nil is treated as "no
	// release published".
	Release mlsidecar.ReleaseSource
	// Platform reports whether the engine exists for this platform. nil
	// means mlsidecar.CurrentPlatformSupport.
	Platform func() (bool, string)
}

var _ SidecarAPI = (*Impl)(nil)

func (s *Impl) platform() (bool, string) {
	if s.Platform != nil {
		return s.Platform()
	}
	return mlsidecar.CurrentPlatformSupport()
}

func (s *Impl) release(ctx context.Context) (mlsidecar.EngineRelease, error) {
	if s.Release == nil {
		return mlsidecar.EngineRelease{}, mlsidecar.ErrNoPublishedRelease
	}
	return s.Release(ctx)
}

// Status implements SidecarAPI.
func (s *Impl) Status(ctx context.Context) (StatusView, error) {
	if s.Manager == nil {
		return s.compose(ctx, mlsidecar.Status{State: mlsidecar.StateNotInstalled}, false), nil
	}
	st, running := s.Manager.Observe(ctx)
	return s.compose(ctx, st, running), nil
}

func (s *Impl) compose(ctx context.Context, st mlsidecar.Status, running bool) StatusView {
	v := StatusView{
		State:         string(st.State),
		Reason:        string(st.Reason),
		Detail:        st.Detail,
		EngineVersion: st.EngineVersion,
	}
	v.Supported, v.UnavailableReason = s.platform()
	if s.Manager != nil {
		v.InstallLocation = s.Manager.Layout.Root
		if rec, ok := s.Manager.Installed(); ok {
			v.Installed = true
			v.InstalledVersion = rec.Version
		}
	}

	rel, relErr := s.release(ctx)
	switch {
	case s.Manager == nil:
		v.Available = false
		if v.UnavailableReason == "" {
			v.UnavailableReason = "Local recommendations need a data directory, which this session does not have."
		}
	case !v.Supported:
		// UnavailableReason already carries the platform sentence.
	case relErr != nil:
		v.UnavailableReason = relErr.Error()
		if errors.Is(relErr, mlsidecar.ErrNoPublishedRelease) {
			v.UnavailableReason = "The Kameas ML engine has not been published to the release channel yet, so it cannot be downloaded in this build."
		}
	default:
		v.Available = true
		v.Release = ReleaseView{Version: rel.Version, SizeMB: rel.SizeMB()}
		if v.Installed && newerThan(rel.Version, v.InstalledVersion) {
			v.UpdateAvailable = true
		}
	}

	// View-only idle state: installed, nothing running, no failure on
	// record. (The engine stops itself when idle; "unhealthy" here would
	// be a false alarm.)
	if v.Installed && !running && st.Reason == mlsidecar.ReasonNone &&
		(st.State == mlsidecar.StateNotInstalled || st.State == mlsidecar.StateInstalledUnhealthy || st.State == mlsidecar.StateHealthy) &&
		!strings.HasPrefix(st.Detail, "install failed") && !strings.HasPrefix(st.Detail, "spawned but") {
		v.State = StateInstalledIdle
	}
	return v
}

// Enable implements SidecarAPI.
func (s *Impl) Enable(ctx context.Context) (StatusView, error) {
	if s.Manager == nil {
		return s.compose(ctx, mlsidecar.Status{State: mlsidecar.StateNotInstalled}, false), ErrUnavailable
	}
	if ok, why := s.platform(); !ok {
		return s.statusNoErr(ctx), fmt.Errorf("%w: %s", ErrUnavailable, why)
	}
	rel, err := s.release(ctx)
	if err != nil {
		return s.statusNoErr(ctx), fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	st := s.Manager.InstallAndActivate(ctx, rel.InstallRequest())
	return s.compose(ctx, st, st.State == mlsidecar.StateHealthy), nil
}

// Update implements SidecarAPI.
func (s *Impl) Update(ctx context.Context) (StatusView, error) {
	if s.Manager == nil {
		return s.compose(ctx, mlsidecar.Status{State: mlsidecar.StateNotInstalled}, false), ErrUnavailable
	}
	rec, ok := s.Manager.Installed()
	if !ok {
		return s.statusNoErr(ctx), ErrNotInstalled
	}
	rel, err := s.release(ctx)
	if err != nil {
		return s.statusNoErr(ctx), fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if !newerThan(rel.Version, rec.Version) {
		return s.statusNoErr(ctx), fmt.Errorf("no newer engine than %s is pinned by this build", rec.Version)
	}
	_, st := s.Manager.UpdateAndActivate(ctx, rel.InstallRequest())
	return s.compose(ctx, st, st.State == mlsidecar.StateHealthy), nil
}

// Repair implements SidecarAPI.
func (s *Impl) Repair(ctx context.Context) (StatusView, error) {
	if s.Manager == nil {
		return s.compose(ctx, mlsidecar.Status{State: mlsidecar.StateNotInstalled}, false), ErrUnavailable
	}
	if _, ok := s.Manager.Installed(); !ok {
		return s.statusNoErr(ctx), ErrNotInstalled
	}
	st := s.Manager.Reconcile(ctx)
	return s.compose(ctx, st, st.State == mlsidecar.StateHealthy), nil
}

// Uninstall implements SidecarAPI.
func (s *Impl) Uninstall(ctx context.Context) (StatusView, error) {
	if s.Manager == nil {
		return s.compose(ctx, mlsidecar.Status{State: mlsidecar.StateNotInstalled}, false), ErrUnavailable
	}
	if err := s.Manager.Uninstall(ctx); err != nil {
		return s.statusNoErr(ctx), fmt.Errorf("uninstall: %w", err)
	}
	return s.compose(ctx, s.Manager.Status(), false), nil
}

func (s *Impl) statusNoErr(ctx context.Context) StatusView {
	v, _ := s.Status(ctx)
	return v
}

// newerThan reports whether candidate is a strictly newer X.Y.Z than
// current. Unparsable versions compare as "different means newer" only
// when they differ (the pin is authoritative); identical strings are never
// newer.
func newerThan(candidate, current string) bool {
	if candidate == "" || candidate == current {
		return false
	}
	a, aok := parseSemver(candidate)
	b, bok := parseSemver(current)
	if !aok || !bok {
		return true
	}
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

func parseSemver(v string) ([3]int, bool) {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	var out [3]int
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
