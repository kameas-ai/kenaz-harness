package fleet

// project_sync.go — project-event tail + per-artifact-class opt-in + audit.
//
// ProjectSyncer parallels SessionSyncer but for project bundles. A "project
// bundle" is the set of events that describe a project: metadata changes,
// notes, agent memory. Binary artifacts are excluded by default (configurable
// via ArtifactClassOptions).
//
// Per-artifact-class opt-in:
//   ArtifactClassOptions{Notes: true, Binaries: false} is the default.
//   Callers pass the class when deciding whether to include an artifact.
//
// Toggle persistence: stored in keychain under
//   fleet:<env>:project:<projectID>:sync_enabled
//
// Privacy invariant: project content bytes are encrypted before leaving this
// layer. No plaintext bytes appear in slog or audit.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zalando/go-keyring"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/paths"
)

// ErrProjectSyncCapabilityRequired is returned when enabling project sync
// requires the context_sync capability the user lacks.
var ErrProjectSyncCapabilityRequired = errors.New("fleet: context_sync capability required to enable project sync")

// ArtifactClass enumerates the artifact types for per-class sync opt-in.
type ArtifactClass string

const (
	// ArtifactClassNotes includes text notes and project documentation.
	ArtifactClassNotes ArtifactClass = "notes"
	// ArtifactClassBinaries includes binary file attachments.
	ArtifactClassBinaries ArtifactClass = "binaries"
	// ArtifactClassMemory includes agent long-term memory blobs.
	ArtifactClassMemory ArtifactClass = "memory"
)

// ArtifactClassOptions configures which artifact classes sync for a project.
// Default: Notes+Memory yes, Binaries no.
type ArtifactClassOptions struct {
	Notes    bool `json:"notes"`
	Binaries bool `json:"binaries"`
	Memory   bool `json:"memory"`
}

// DefaultArtifactClassOptions returns the default opt-in set.
func DefaultArtifactClassOptions() ArtifactClassOptions {
	return ArtifactClassOptions{Notes: true, Binaries: false, Memory: true}
}

// ProjectEventRecord is the opaque payload unit for a project event.
// Bytes is encrypted-ready; ArtifactClass is metadata only (never logged).
//
// Privacy invariant: Bytes is NEVER logged.
type ProjectEventRecord struct {
	Seq           uint64
	Bytes         []byte
	ArtifactClass ArtifactClass // empty for non-artifact events
}

// ProjectSyncer bridges project events to an EventStream.
type ProjectSyncer struct {
	client  *Client
	emitter contextaudit.Emitter
	caps    *Capabilities
}

// NewProjectSyncer constructs a ProjectSyncer.
func NewProjectSyncer(client *Client, emitter contextaudit.Emitter, caps *Capabilities) *ProjectSyncer {
	return &ProjectSyncer{client: client, emitter: emitter, caps: caps}
}

// EnableSync enables fleet sync for projectID. Backfills existingEvents that
// pass the artifactClassOpts filter.
func (ps *ProjectSyncer) EnableSync(ctx context.Context, projectID string, existingEvents []ProjectEventRecord, opts ArtifactClassOptions) error {
	if ps.client == nil || ps.client.isNop {
		return ErrFleetDisabled
	}
	if ps.caps != nil && !ps.caps.Has(CapContextSync) {
		return ErrProjectSyncCapabilityRequired
	}

	seed, err := SeedKey()
	if err != nil {
		return fmt.Errorf("fleet: project sync enable: seed: %w", err)
	}
	key, err := DeriveKey(seed, LabelProjectEvents)
	if err != nil {
		return fmt.Errorf("fleet: project sync enable: derive key: %w", err)
	}

	streamID := projectStreamID(projectID)
	es, err := NewEventStream(ps.client, streamID, key)
	if err != nil {
		return fmt.Errorf("fleet: project sync enable: stream: %w", err)
	}

	// Filter events by artifact class and backfill.
	var batch []Event
	for _, r := range existingEvents {
		if r.ArtifactClass != "" && !artifactClassAllowed(r.ArtifactClass, opts) {
			continue
		}
		batch = append(batch, Event{Seq: r.Seq, Payload: r.Bytes})
	}
	if len(batch) > 0 {
		if err := es.Backfill(ctx, batch); err != nil {
			return fmt.Errorf("fleet: project sync backfill: %w", err)
		}
	}

	// Persist toggle + artifact class options.
	if err := setProjectSyncEnabled(projectID, true); err != nil {
		return fmt.Errorf("fleet: project sync persist toggle: %w", err)
	}
	if err := setProjectArtifactOpts(projectID, opts); err != nil {
		// Non-fatal: default will apply on next read.
		logging.L().Warn("fleet.project_sync.persist_opts_failed", "err", err.Error())
	}

	logging.L().Info("fleet.project_sync.enabled",
		"project_id", shortID(projectID),
		"stream_id", shortID(streamID),
		"backfill_count", len(batch),
	)
	ps.emitAudit(ctx, contextaudit.KindFleetProjectSyncEnabled, contextaudit.FleetProjectSyncPayload{
		ProjectID: projectID,
		StreamID:  streamID,
	})
	return nil
}

// DisableSync disables fleet sync for projectID. Does not delete remote events.
func (ps *ProjectSyncer) DisableSync(ctx context.Context, projectID string) error {
	if ps.client == nil || ps.client.isNop {
		return ErrFleetDisabled
	}

	if err := setProjectSyncEnabled(projectID, false); err != nil {
		return fmt.Errorf("fleet: project sync disable: %w", err)
	}

	streamID := projectStreamID(projectID)
	logging.L().Info("fleet.project_sync.disabled", "project_id", shortID(projectID))
	ps.emitAudit(ctx, contextaudit.KindFleetProjectSyncDisabled, contextaudit.FleetProjectSyncPayload{
		ProjectID: projectID,
		StreamID:  streamID,
	})
	return nil
}

// AppendEvent encrypts and appends a project event to fleet, subject to the
// persisted artifact class options. Artifacts excluded by opts are silently
// skipped.
func (ps *ProjectSyncer) AppendEvent(ctx context.Context, projectID string, record ProjectEventRecord) error {
	if ps.client == nil || ps.client.isNop {
		return ErrFleetDisabled
	}
	if !isProjectSyncEnabled(projectID) {
		return nil
	}

	opts := loadProjectArtifactOpts(projectID)
	if record.ArtifactClass != "" && !artifactClassAllowed(record.ArtifactClass, opts) {
		return nil // excluded by class opt-in
	}

	seed, err := LoadContextSeed()
	if err != nil {
		return fmt.Errorf("fleet: project append event: load seed: %w", err)
	}
	key, err := DeriveKey(seed, LabelProjectEvents)
	if err != nil {
		return fmt.Errorf("fleet: project append event: derive key: %w", err)
	}

	streamID := projectStreamID(projectID)
	es, err := NewEventStream(ps.client, streamID, key)
	if err != nil {
		return fmt.Errorf("fleet: project append event: stream: %w", err)
	}
	return es.Append(ctx, []Event{{Seq: record.Seq, Payload: record.Bytes}})
}

// Resume replays fleet events for projectID from sinceSeq. Used on first open
// of a project on a new device.
func (ps *ProjectSyncer) Resume(ctx context.Context, projectID string, sinceSeq uint64, apply func(ProjectEventRecord) error) error {
	if ps.client == nil || ps.client.isNop {
		return ErrFleetDisabled
	}

	seed, err := LoadContextSeed()
	if err != nil {
		return fmt.Errorf("fleet: project resume: load seed: %w", err)
	}
	key, err := DeriveKey(seed, LabelProjectEvents)
	if err != nil {
		return fmt.Errorf("fleet: project resume: derive key: %w", err)
	}

	streamID := projectStreamID(projectID)
	es, err := NewEventStream(ps.client, streamID, key)
	if err != nil {
		return fmt.Errorf("fleet: project resume: stream: %w", err)
	}

	var count int
	if err := es.Replay(ctx, sinceSeq, func(ev Event) error {
		count++
		return apply(ProjectEventRecord{Seq: ev.Seq, Bytes: ev.Payload})
	}); err != nil {
		return fmt.Errorf("fleet: project resume replay: %w", err)
	}
	logging.L().Info("fleet.project_sync.resumed",
		"project_id", shortID(projectID),
		"events_replayed", count,
	)
	return nil
}

// DeleteRemote purges all fleet events for projectID.
func (ps *ProjectSyncer) DeleteRemote(ctx context.Context, projectID string) error {
	if ps.client == nil || ps.client.isNop {
		return ErrFleetDisabled
	}

	seed, err := LoadContextSeed()
	if err != nil {
		return fmt.Errorf("fleet: project delete remote: load seed: %w", err)
	}
	key, err := DeriveKey(seed, LabelProjectEvents)
	if err != nil {
		return fmt.Errorf("fleet: project delete remote: derive key: %w", err)
	}

	streamID := projectStreamID(projectID)
	es, err := NewEventStream(ps.client, streamID, key)
	if err != nil {
		return fmt.Errorf("fleet: project delete remote: stream: %w", err)
	}

	if err := es.DeleteRemote(ctx); err != nil {
		return err
	}
	_ = setProjectSyncEnabled(projectID, false) // best-effort

	logging.L().Info("fleet.project_sync.remote_deleted", "project_id", shortID(projectID))
	ps.emitAudit(ctx, contextaudit.KindFleetProjectSyncRemoteDeleted, contextaudit.FleetProjectSyncPayload{
		ProjectID: projectID,
		StreamID:  streamID,
	})
	return nil
}

// SetArtifactClassOptions updates the per-class opt-in for projectID.
func (ps *ProjectSyncer) SetArtifactClassOptions(projectID string, opts ArtifactClassOptions) error {
	return setProjectArtifactOpts(projectID, opts)
}

// GetArtifactClassOptions returns the current per-class opt-in for projectID.
func (ps *ProjectSyncer) GetArtifactClassOptions(projectID string) ArtifactClassOptions {
	return loadProjectArtifactOpts(projectID)
}

// IsSyncEnabled returns whether fleet sync is currently enabled for projectID.
func (ps *ProjectSyncer) IsSyncEnabled(projectID string) bool {
	return isProjectSyncEnabled(projectID)
}

// ── persistence helpers ───────────────────────────────────────────────────────

func projectStreamID(projectID string) string {
	return "proj:" + projectID
}

func projectSyncKey(projectID string) string {
	return "fleet:" + paths.KeychainNamespace() + ":project:" + projectID + ":sync_enabled"
}

func projectArtifactOptsKey(projectID string) string {
	return "fleet:" + paths.KeychainNamespace() + ":project:" + projectID + ":artifact_opts"
}

func setProjectSyncEnabled(projectID string, enabled bool) error {
	svc := paths.FleetKeychainService()
	val := "0"
	if enabled {
		val = "1"
	}
	if err := keyring.Set(svc, projectSyncKey(projectID), val); err != nil {
		return fmt.Errorf("fleet: set project sync flag: %w", err)
	}
	return nil
}

func isProjectSyncEnabled(projectID string) bool {
	svc := paths.FleetKeychainService()
	val, err := keyring.Get(svc, projectSyncKey(projectID))
	if err != nil {
		return false
	}
	return val == "1"
}

func setProjectArtifactOpts(projectID string, opts ArtifactClassOptions) error {
	svc := paths.FleetKeychainService()
	b, err := json.Marshal(opts)
	if err != nil {
		return fmt.Errorf("fleet: marshal artifact opts: %w", err)
	}
	if err := keyring.Set(svc, projectArtifactOptsKey(projectID), string(b)); err != nil {
		return fmt.Errorf("fleet: set artifact opts: %w", err)
	}
	return nil
}

func loadProjectArtifactOpts(projectID string) ArtifactClassOptions {
	svc := paths.FleetKeychainService()
	raw, err := keyring.Get(svc, projectArtifactOptsKey(projectID))
	if err != nil || raw == "" {
		return DefaultArtifactClassOptions()
	}
	var opts ArtifactClassOptions
	if err := json.Unmarshal([]byte(raw), &opts); err != nil {
		return DefaultArtifactClassOptions()
	}
	return opts
}

// artifactClassAllowed returns whether the given artifact class is permitted
// by the options.
func artifactClassAllowed(class ArtifactClass, opts ArtifactClassOptions) bool {
	switch class {
	case ArtifactClassNotes:
		return opts.Notes
	case ArtifactClassBinaries:
		return opts.Binaries
	case ArtifactClassMemory:
		return opts.Memory
	default:
		return true // unknown classes default to allowed
	}
}

// ── audit helper ──────────────────────────────────────────────────────────────

func (ps *ProjectSyncer) emitAudit(ctx context.Context, kind contextaudit.Kind, payload any) {
	if ps.emitter == nil {
		return
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		logging.L().Warn("fleet.project_sync.audit_marshal_failed", "kind", string(kind), "err", err.Error())
		return
	}
	_ = ps.emitter.Emit(ctx, contextaudit.Event{
		Kind:    kind,
		TS:      time.Now().UTC(),
		Payload: raw,
	})
}
