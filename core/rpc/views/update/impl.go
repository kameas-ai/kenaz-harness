// Concrete UpdateAPI implementation backed by core/update.Service.
package update

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
	coreupdate "github.com/kameas-ai/kenaz-harness/core/update"
)

// ErrServiceUnavailable is returned by every state-mutating method
// when the chassis booted without a Service wired (test path).
var ErrServiceUnavailable = errors.New("update: service unavailable")

// ErrNoUpdateAvailable is returned by StartDownload when no actionable
// Info has been observed yet (call StartCheck first).
var ErrNoUpdateAvailable = errors.New("update: no actionable update available")

// ErrNothingStaged is returned by Apply when no successful download
// has produced a StagedUpdate yet.
var ErrNothingStaged = errors.New("update: nothing staged for apply")

// ErrDownloadInFlight is returned by StartDownload when a previous
// download is still running. The caller should wait for completion
// (TopicDownloadComplete / TopicDownloadFailed) before retrying.
var ErrDownloadInFlight = errors.New("update: download already in progress")

// ProgressPublisher is the broker-emit surface the manager fans
// progress events onto. Decoupled from rpc.StreamBroker so the view
// stays import-clean and trivially testable.
type ProgressPublisher interface {
	Publish(topic string, payload any)
}

// Download state values surfaced via StatusOutput.
const (
	stateIdle        = "idle"
	stateDownloading = "downloading"
	stateStaged      = "staged"
	stateFailed      = "failed"
)

// Manager is the concrete UpdateAPI. Wraps a core/update.Service plus
// a small in-memory state mirror so Status reads are cheap and don't
// touch the network. All mutations are mutex-guarded; concurrent
// readers (Status) and writers (the StartDownload goroutine) are
// expected on every install.
type Manager struct {
	svc       coreupdate.Service
	publisher ProgressPublisher

	// CurrentVersion is captured at construction so Status can return
	// a populated value even before StartCheck has fired. Kept on the
	// Manager rather than fetched from the Service every call to
	// avoid a state-machine round-trip for the most common UI path.
	currentVersion string

	mu             sync.RWMutex
	info           coreupdate.Info // last successful Check result
	hasInfo        bool
	state          string
	progressBytes  int64
	progressTotal  int64
	progressPct    int
	staged         coreupdate.StagedUpdate
	hasStaged      bool
	lastCheckedAt  int64
	downloadActive bool
	// downloadErr carries the reason the most recent download attempt
	// failed (set by markFailed, cleared by StartDownload). Mirrored
	// out via StatusOutput.DownloadError so a fresh Status call after
	// a reload can still explain a "failed" state (FR-004/FR-005) —
	// the one-shot TopicDownloadFailed broker frame is not the only
	// source of the reason.
	downloadErr string
}

// Config bundles the Manager's dependencies.
type Config struct {
	// Service is the core/update.Service instance. nil makes the
	// Manager surface ErrServiceUnavailable on every state-mutating
	// call. Status still works (returns a zero StatusOutput).
	Service coreupdate.Service
	// Publisher is the broker-emit surface used to fan download
	// progress / completion / failure events. nil disables broker
	// publishes (Manager keeps its in-memory state up-to-date).
	Publisher ProgressPublisher
	// CurrentVersion is the running build's version string. Surfaced
	// in StatusOutput.CurrentVersion so the frontend can render the
	// "you're on vX.Y.Z" pill before the first Check fires.
	CurrentVersion string
}

// New constructs a Manager. Safe for nil Service / Publisher.
func New(cfg Config) *Manager {
	return &Manager{
		svc:            cfg.Service,
		publisher:      cfg.Publisher,
		currentVersion: cfg.CurrentVersion,
		state:          stateIdle,
	}
}

// Status returns the current wire view. Cheap reader; does not touch
// the network or the on-disk skip file.
func (m *Manager) Status(_ context.Context) (StatusOutput, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := StatusOutput{
		CurrentVersion: m.currentVersion,
		Channel:        "stable",
		DownloadState:  m.state,
		LastCheckedAt:  m.lastCheckedAt,
	}
	if m.state == stateDownloading {
		out.DownloadProgress = m.progressPct
	}
	// DownloadError is copied unconditionally (not gated on
	// state==stateFailed the way DownloadProgress is gated on
	// stateDownloading): StartDownload clears it up front (below), so a
	// non-failed state always reads back "" on its own — gating here
	// would just mask a regression that stops clearing it, which is
	// exactly the WP01 mutation this field exists to catch.
	out.DownloadError = m.downloadErr
	if m.hasInfo {
		out.Available = m.info.Available && !m.info.SkippedByUser
		out.AvailableVersion = m.info.AvailableVersion
		if m.info.Channel != "" {
			out.Channel = m.info.Channel
		}
		out.Notes = m.info.Notes
		if looksLikeURL(m.info.Notes) {
			out.ReleaseURL = m.info.Notes
		}
		out.SkippedByUser = m.info.SkippedByUser
	}
	return out, nil
}

// StartCheck triggers a synchronous Service.Check and folds the result
// into the in-memory mirror.
func (m *Manager) StartCheck(ctx context.Context) error {
	if m == nil || m.svc == nil {
		return ErrServiceUnavailable
	}
	info, err := m.svc.Check(ctx)
	if err != nil {
		// Privacy: log the error but never the manifest body content.
		logging.L().Warn("update.view.check.failed", "err", err.Error())
		return err
	}
	m.mu.Lock()
	m.info = info
	m.hasInfo = true
	m.lastCheckedAt = time.Now().Unix()
	m.mu.Unlock()
	logging.L().Info("update.view.check.ok",
		"available", info.Available,
		"skipped", info.SkippedByUser,
	)
	return nil
}

// StartDownload spawns a goroutine that drains the Service.Download
// progress channel. Returns ErrDownloadInFlight if a previous download
// is still running (the manager only tracks one at a time).
func (m *Manager) StartDownload(ctx context.Context) error {
	if m == nil || m.svc == nil {
		return ErrServiceUnavailable
	}
	m.mu.Lock()
	if m.downloadActive {
		m.mu.Unlock()
		return ErrDownloadInFlight
	}
	if !m.hasInfo || m.info.DownloadURL == "" {
		m.mu.Unlock()
		return ErrNoUpdateAvailable
	}
	info := m.info
	m.downloadActive = true
	m.state = stateDownloading
	m.progressBytes = 0
	m.progressTotal = 0
	m.progressPct = 0
	m.hasStaged = false
	m.downloadErr = ""
	m.mu.Unlock()

	progress, staged, err := m.svc.Download(ctx, info)
	if err != nil {
		m.markFailed(err)
		return err
	}
	go m.drainProgress(progress, staged)
	logging.L().Info("update.view.download.start", "platform", staged.Platform)
	return nil
}

// drainProgress consumes the Service.Download channel, updates the
// in-memory mirror, and fans events on the broker. Owned by the
// goroutine spawned in StartDownload.
func (m *Manager) drainProgress(progress <-chan coreupdate.DownloadProgress, staged coreupdate.StagedUpdate) {
	for tick := range progress {
		if !tick.Done {
			pct := percent(tick.Bytes, tick.Total)
			m.mu.Lock()
			m.progressBytes = tick.Bytes
			m.progressTotal = tick.Total
			m.progressPct = pct
			m.mu.Unlock()
			m.publish(TopicDownloadProgress, DownloadProgressPayload{
				Bytes:   tick.Bytes,
				Total:   tick.Total,
				Percent: pct,
			})
			continue
		}
		// Final tick. tick.Err non-nil → failed; nil → success.
		if tick.Err != nil {
			m.markFailed(tick.Err)
			return
		}
		m.mu.Lock()
		m.staged = staged
		m.hasStaged = true
		m.state = stateStaged
		m.progressPct = 100
		m.progressBytes = tick.Bytes
		m.progressTotal = tick.Total
		m.downloadActive = false
		m.mu.Unlock()
		m.publish(TopicDownloadComplete, StagedPayload{TargetVersion: staged.TargetVersion})
		logging.L().Info("update.view.download.complete")
		return
	}
	// Channel closed without a Done tick — treat as failure.
	m.markFailed(errors.New("download channel closed unexpectedly"))
}

// markFailed updates state to "failed" and publishes the failure
// event. Privacy: only the error message goes on the wire; we do not
// include the manifest version (which would leak release-notes-shaped
// strings into the audit channel).
func (m *Manager) markFailed(err error) {
	m.mu.Lock()
	m.state = stateFailed
	m.progressPct = 0
	m.downloadActive = false
	m.hasStaged = false
	m.downloadErr = err.Error()
	m.mu.Unlock()
	m.publish(TopicDownloadFailed, DownloadFailedPayload{Err: err.Error()})
	logging.L().Warn("update.view.download.failed", "err", err.Error())
}

// Apply consumes the most recent staged download and asks the service
// to swap + restart.
func (m *Manager) Apply(ctx context.Context) error {
	if m == nil || m.svc == nil {
		return ErrServiceUnavailable
	}
	m.mu.RLock()
	staged := m.staged
	hasStaged := m.hasStaged
	m.mu.RUnlock()
	if !hasStaged {
		return ErrNothingStaged
	}
	logging.L().Info("update.view.apply.begin", "platform", staged.Platform)
	if err := m.svc.ApplyAndRestart(ctx, staged); err != nil {
		logging.L().Warn("update.view.apply.failed", "err", err.Error())
		return err
	}
	return nil
}

// SkipVersion forwards to Service.SkipVersion and refreshes the
// in-memory mirror so the next Status call reports SkippedByUser=true.
func (m *Manager) SkipVersion(ctx context.Context, version string) error {
	if m == nil || m.svc == nil {
		return ErrServiceUnavailable
	}
	if err := m.svc.SkipVersion(ctx, version); err != nil {
		return err
	}
	m.mu.Lock()
	if m.hasInfo && m.info.AvailableVersion == version {
		m.info.SkippedByUser = true
	}
	m.mu.Unlock()
	logging.L().Info("update.view.skip", "skipped", true)
	return nil
}

// ListSkippedVersions reads the persisted skip set. The Service
// interface does not expose a list method (skips are private to the
// service); we read the on-disk file via the same helper the Service
// uses. To avoid leaking the file path out of the update package, we
// use a typed accessor on the concrete service when available; fall
// back to an empty slice otherwise.
func (m *Manager) ListSkippedVersions(_ context.Context) ([]string, error) {
	if m == nil || m.svc == nil {
		return nil, ErrServiceUnavailable
	}
	// The Service interface intentionally hides the skip list to keep
	// the boundary minimal; implementations that want to expose the
	// list satisfy the optional skipLister interface below. The
	// production *service does — wired in core/update.
	if l, ok := m.svc.(skipLister); ok {
		return l.ListSkipped(), nil
	}
	return []string{}, nil
}

// UnskipVersion removes a version from the skip set. Like
// ListSkippedVersions, this is an optional capability on the service
// implementation.
func (m *Manager) UnskipVersion(ctx context.Context, version string) error {
	if m == nil || m.svc == nil {
		return ErrServiceUnavailable
	}
	u, ok := m.svc.(skipUnsetter)
	if !ok {
		return fmt.Errorf("update: service does not support unskip")
	}
	if err := u.UnskipVersion(ctx, version); err != nil {
		return err
	}
	m.mu.Lock()
	if m.hasInfo && m.info.AvailableVersion == version {
		m.info.SkippedByUser = false
	}
	m.mu.Unlock()
	return nil
}

// publish is a small helper that no-ops when no broker is wired.
func (m *Manager) publish(topic string, payload any) {
	if m.publisher == nil {
		return
	}
	m.publisher.Publish(topic, payload)
}

// skipLister is the optional capability a Service impl can expose so
// the view layer can read the persisted skip set without re-opening
// the file itself.
type skipLister interface {
	ListSkipped() []string
}

// skipUnsetter is the optional capability for UnskipVersion.
type skipUnsetter interface {
	UnskipVersion(ctx context.Context, version string) error
}

// percent computes a 0-100 progress value. total<=0 (server didn't send
// Content-Length) returns 0 so the frontend can render an indeterminate
// spinner rather than a fake "0%".
func percent(bytes, total int64) int {
	if total <= 0 || bytes <= 0 {
		return 0
	}
	if bytes >= total {
		return 100
	}
	return int((bytes * 100) / total)
}

// looksLikeURL is a cheap heuristic for "the manifest stuffed a URL
// in the notes field". We surface the URL as ReleaseURL when it does
// so the frontend can render a "see what's new" link without parsing
// notes itself.
func looksLikeURL(s string) bool {
	return strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://")
}
