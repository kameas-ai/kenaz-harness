package nodes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corenodes "github.com/kameas-ai/kenaz-harness/core/agentgraph/nodes"
)

// Manager owns the resolved-manifest catalog plus the user-override
// directory the chassis configured at boot. The view's Impl reads
// through this manager so reloads + provenance lookups share state.
//
// All public methods are safe for concurrent use.
type Manager struct {
	mu sync.RWMutex
	// catalog is the live, resolved catalog. Swapped atomically by
	// ReloadOverrides under the write lock.
	catalog *corenodes.Catalog
	// snapshot caches the resolved-id set for diffing on reload.
	idSet map[string]string // id → hash (used to detect "modified")
	// userDir is the on-disk directory scanned for overrides. Empty
	// disables overrides.
	userDir string
	// reloadedAt records the most recent successful reload timestamp.
	reloadedAt time.Time
	// hotReload reflects the chassis-level --enable-manifest-hot-reload
	// flag. Reported by the doctor surface; not load-bearing inside the
	// manager itself.
	hotReload bool
	// lastErrors caches the most recent per-file parse errors so the
	// doctor surface can report them without re-scanning the disk.
	lastErrors []string
	// nowFn is overridable for tests.
	nowFn func() time.Time
}

// ManagerConfig is what NewManager consumes.
type ManagerConfig struct {
	// Catalog is the initial catalog (typically produced at chassis
	// boot via corenodes.LoadCatalog). nil falls back to an empty
	// catalog so the chassis still boots; ReloadOverrides re-populates.
	Catalog *corenodes.Catalog
	// UserDir is the directory scanned by ReloadOverrides /
	// ListUserOverrides. Empty disables those operations (no-op).
	UserDir string
	// HotReloadEnabled reflects whether the chassis was started with
	// --enable-manifest-hot-reload. The doctor surface reports this so
	// the frontend can disable / enable diagnostic affordances. The
	// flag is informational; it does not affect manager behaviour.
	HotReloadEnabled bool
	// NowFn is an optional clock injection for tests.
	NowFn func() time.Time
}

// NewManager wires the catalog + user-override directory. nil cfg.Catalog
// is allowed; methods then surface the empty-state behaviour.
func NewManager(cfg ManagerConfig) *Manager {
	cat := cfg.Catalog
	if cat == nil {
		cat = corenodes.NewEmptyCatalog()
	}
	now := cfg.NowFn
	if now == nil {
		now = time.Now
	}
	m := &Manager{
		catalog:   cat,
		userDir:   cfg.UserDir,
		hotReload: cfg.HotReloadEnabled,
		nowFn:     now,
	}
	m.idSet = snapshotIDs(cat)
	return m
}

// Catalog returns a stable pointer to the underlying *corenodes.Catalog.
// Callers that mutate the catalog MUST go through ReloadOverrides;
// direct mutation breaks the diffing contract.
func (m *Manager) Catalog() *corenodes.Catalog {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.catalog
}

// UserDir reports the configured user-override directory.
func (m *Manager) UserDir() string {
	if m == nil {
		return ""
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.userDir
}

// SwapCatalog atomically replaces the catalog. Used by the hot-reload
// watcher. Returns the diff against the prior id-set.
func (m *Manager) SwapCatalog(next *corenodes.Catalog) ReloadResult {
	if m == nil || next == nil {
		return ReloadResult{}
	}
	m.mu.Lock()
	prior := m.idSet
	m.catalog = next
	m.idSet = snapshotIDs(next)
	m.reloadedAt = m.nowFn()
	res := diffSnapshots(prior, m.idSet)
	res.ReloadedAt = m.reloadedAt.UTC().Format(time.RFC3339Nano)
	m.mu.Unlock()
	return res
}

// recordLastErrors stashes the per-file error list from the most recent
// reload so the doctor surface can report it without re-scanning disk.
// Empty slice clears any prior errors.
func (m *Manager) recordLastErrors(errs []string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(errs) == 0 {
		m.lastErrors = nil
		return
	}
	m.lastErrors = append([]string(nil), errs...)
}

// doctor builds a DoctorReport snapshot under the read lock. Counters
// derive from the live catalog; bookkeeping fields (last reload, hot-
// reload flag, recorded per-file errors) come from the manager state.
func (m *Manager) doctor() DoctorReport {
	if m == nil {
		return DoctorReport{SunsetVersion: coreag.AliasSunsetVersion}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	rep := DoctorReport{
		UserDir:          m.userDir,
		HotReloadEnabled: m.hotReload,
		SunsetVersion:    coreag.AliasSunsetVersion,
	}
	if m.catalog != nil {
		for _, rm := range m.catalog.List() {
			if strings.HasPrefix(rm.Source, "user:") {
				rep.UserOverrideCount++
			} else {
				rep.ShippedCount++
			}
			if rm.Manifest.IsArchetype() {
				rep.ArchetypeCount++
			}
			if rm.Manifest.IsCallable() {
				rep.CallableCount++
			}
			rep.AliasCount += len(rm.Manifest.Aliases)
		}
	}
	if !m.reloadedAt.IsZero() {
		rep.LastReloadAt = m.reloadedAt.UTC().Format(time.RFC3339Nano)
	}
	if len(m.lastErrors) > 0 {
		rep.UserOverrideErrors = append([]string(nil), m.lastErrors...)
	}
	return rep
}

// snapshotIDs builds the id→hash map used to compute reload diffs.
// hash mirrors the catalog entry's content hash (post-merge).
func snapshotIDs(cat *corenodes.Catalog) map[string]string {
	out := map[string]string{}
	if cat == nil {
		return out
	}
	for _, rm := range cat.List() {
		out[rm.Manifest.ID] = rm.Hash
	}
	return out
}

// diffSnapshots computes the added / removed / modified id sets.
func diffSnapshots(prior, next map[string]string) ReloadResult {
	res := ReloadResult{}
	for id, h := range next {
		ph, ok := prior[id]
		switch {
		case !ok:
			res.Added = append(res.Added, id)
		case ph != h:
			res.Modified = append(res.Modified, id)
		}
	}
	for id := range prior {
		if _, ok := next[id]; !ok {
			res.Removed = append(res.Removed, id)
		}
	}
	sort.Strings(res.Added)
	sort.Strings(res.Removed)
	sort.Strings(res.Modified)
	return res
}

// Impl is the concrete NodesAPI implementation backed by Manager.
type Impl struct {
	mgr *Manager
}

// New constructs the API. nil manager is allowed; methods return
// ErrManagerUnavailable so the chassis can boot without a catalog
// wired and the frontend renders the empty state.
func New(mgr *Manager) *Impl { return &Impl{mgr: mgr} }

// Catalog implements NodesAPI.
func (a *Impl) Catalog(_ context.Context) ([]NodeManifestSummary, error) {
	if a == nil || a.mgr == nil {
		return []NodeManifestSummary{}, ErrManagerUnavailable
	}
	cat := a.mgr.Catalog()
	if cat == nil {
		return []NodeManifestSummary{}, ErrManagerUnavailable
	}
	rows := cat.List()
	out := make([]NodeManifestSummary, 0, len(rows))
	for _, rm := range rows {
		out = append(out, summaryFrom(rm))
	}
	return out, nil
}

// Get implements NodesAPI.
func (a *Impl) Get(_ context.Context, id string) (NodeManifestDetail, error) {
	if a == nil || a.mgr == nil {
		return NodeManifestDetail{}, ErrManagerUnavailable
	}
	cat := a.mgr.Catalog()
	if cat == nil {
		return NodeManifestDetail{}, ErrManagerUnavailable
	}
	rm, err := cat.Get(id)
	if err != nil {
		return NodeManifestDetail{}, err
	}
	return detailFrom(rm), nil
}

// ReloadOverrides implements NodesAPI. Scans the user directory, parses
// each YAML, deep-merges over the shipped manifests, and atomically
// swaps the live catalog when the resolve succeeds. Per-file parse
// errors are surfaced in ReloadResult.Errors but do not abort the
// successful resolve path.
func (a *Impl) ReloadOverrides(_ context.Context) (ReloadResult, error) {
	if a == nil || a.mgr == nil {
		return ReloadResult{}, ErrManagerUnavailable
	}
	userDir := a.mgr.UserDir()
	cat, loadErr := corenodes.LoadCatalog(corenodes.LoadOptions{UserDir: userDir})
	// LoadCatalog returns the resolved catalog AND a bundled error
	// describing per-file parse failures; we still want to atomically
	// swap when at least the bundled set resolved cleanly.
	res := ReloadResult{}
	if cat != nil {
		res = a.mgr.SwapCatalog(cat)
	}
	if loadErr != nil {
		res.Errors = append(res.Errors, loadErr.Error())
	}
	a.mgr.recordLastErrors(res.Errors)
	if loadErr != nil {
		return res, nil
	}
	return res, nil
}

// Doctor implements NodesAPI. Returns a one-shot summary of catalog
// health for the frontend NodesView debug panel (WP08). Counters are
// derived from the live catalog under a read lock; the report mutates
// no state.
func (a *Impl) Doctor(_ context.Context) (DoctorReport, error) {
	if a == nil || a.mgr == nil {
		return DoctorReport{
			SunsetVersion: coreag.AliasSunsetVersion,
		}, ErrManagerUnavailable
	}
	return a.mgr.doctor(), nil
}

// ListUserOverrides implements NodesAPI. Reads the configured user
// directory and returns one row per *.yaml file with parse status.
// Missing directory and empty directory are both reported as an empty
// slice with no error.
func (a *Impl) ListUserOverrides(_ context.Context) ([]UserOverrideInfo, error) {
	if a == nil || a.mgr == nil {
		return []UserOverrideInfo{}, ErrManagerUnavailable
	}
	dir := a.mgr.UserDir()
	if dir == "" {
		return []UserOverrideInfo{}, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []UserOverrideInfo{}, nil
		}
		return []UserOverrideInfo{}, fmt.Errorf("nodes: read user dir %q: %w", dir, err)
	}
	out := make([]UserOverrideInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".yaml") {
			continue
		}
		full := filepath.Join(dir, name)
		info := UserOverrideInfo{
			Path:     full,
			Filename: name,
			Status:   "ok",
		}
		if fi, statErr := e.Info(); statErr == nil {
			info.SizeBytes = fi.Size()
		}
		// We try a best-effort load to pick up the manifest id +
		// surface any parse error. A successful merge is gated by
		// ReloadOverrides; this is a status surface only.
		probe, perr := corenodes.LoadCatalog(corenodes.LoadOptions{
			SkipBundled: true,
			UserDir:     dir,
		})
		_ = probe
		if perr != nil && strings.Contains(perr.Error(), name) {
			info.Status = "error"
			info.Error = perr.Error()
		} else if probe != nil {
			// Probe id by checking which entry in the probe matches
			// the filename stem.
			stem := strings.TrimSuffix(strings.ToLower(name), ".yaml")
			stem = strings.TrimPrefix(stem, "_archetype.")
			if rm, gErr := probe.Get(stem); gErr == nil && rm != nil {
				info.ID = rm.Manifest.ID
			}
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Filename < out[j].Filename })
	return out, nil
}

// summaryFrom projects a *corenodes.ResolvedManifest onto the wire
// summary shape.
func summaryFrom(rm *corenodes.ResolvedManifest) NodeManifestSummary {
	if rm == nil {
		return NodeManifestSummary{}
	}
	m := rm.Manifest
	return NodeManifestSummary{
		ID:          m.ID,
		KindName:    m.KindName,
		DisplayName: m.DisplayName,
		Description: m.Description,
		Category:    string(m.Category),
		Extends:     m.Extends,
		Archetype:   m.Archetype,
		Callable:    m.IsCallable(),
		Aliases:     append([]string(nil), m.Aliases...),
		Source:      rm.Source,
		Hash:        rm.Hash,
		Version:     m.Version,
	}
}

// detailFrom projects the full resolved manifest into the wire detail
// shape (including per-field provenance).
func detailFrom(rm *corenodes.ResolvedManifest) NodeManifestDetail {
	if rm == nil {
		return NodeManifestDetail{}
	}
	m := rm.Manifest

	// Attrs in deterministic order so the frontend renders fields
	// consistently across reloads.
	attrNames := make([]string, 0, len(m.Attrs))
	for name := range m.Attrs {
		attrNames = append(attrNames, name)
	}
	sort.Strings(attrNames)
	attrs := make([]AttrSpecWire, 0, len(attrNames))
	for _, name := range attrNames {
		spec := m.Attrs[name]
		attrs = append(attrs, AttrSpecWire{
			Name:        name,
			Type:        string(spec.Type),
			Required:    spec.Required,
			Default:     spec.Default,
			Enum:        append([]string(nil), spec.Enum...),
			Min:         clonePtrFloat(spec.Min),
			Max:         clonePtrFloat(spec.Max),
			MinLength:   clonePtrInt(spec.MinLength),
			MaxLength:   clonePtrInt(spec.MaxLength),
			Description: spec.Description,
		})
	}
	ports := PortSetWire{
		Inputs:  portsToWire(m.Ports.Inputs),
		Outputs: portsToWire(m.Ports.Outputs),
	}
	// Provenance: re-key into the wire format. The map is small so
	// constructing a sorted slice is fine.
	provKeys := make([]string, 0, len(rm.Provenance))
	for k := range rm.Provenance {
		provKeys = append(provKeys, k)
	}
	sort.Strings(provKeys)
	prov := make([]AttrProvenance, 0, len(provKeys))
	for _, k := range provKeys {
		prov = append(prov, AttrProvenance{
			FieldPath: k,
			Layer:     normaliseLayer(rm.Provenance[k], m.ID, rm.Source),
		})
	}
	defaults := map[string]interface{}{}
	for k, v := range m.Defaults {
		defaults[k] = v
	}
	return NodeManifestDetail{
		Summary:        summaryFrom(rm),
		Chain:          append([]string(nil), rm.Chain...),
		Attrs:          attrs,
		Ports:          ports,
		Defaults:       defaults,
		BudgetConsumes: append([]string(nil), m.BudgetConsumes...),
		Budget:         string(m.Budget),
		Executor:       m.Executor,
		Provenance:     prov,
	}
}

// normaliseLayer maps a raw layer-id (the manifest id of the
// contributing layer) into the wire-stable label set:
//
//   - If the layer matches the leaf id AND the source carries the
//     "user:" prefix, surface "user-override".
//   - If the layer is the leaf id (and not user-supplied), surface
//     "shipped".
//   - Otherwise the layer is an inherited archetype: surface
//     "archetype-<id>".
func normaliseLayer(rawLayer, leafID, source string) string {
	if rawLayer == leafID {
		if strings.HasPrefix(source, "user:") {
			return LayerUserOverride
		}
		return LayerShipped
	}
	return LayerArchetypePrefix + rawLayer
}

func portsToWire(in []corenodes.PortSpec) []PortSpecWire {
	if len(in) == 0 {
		return nil
	}
	out := make([]PortSpecWire, 0, len(in))
	for _, p := range in {
		out = append(out, PortSpecWire{
			Name:        p.Name,
			Type:        p.Type,
			Description: p.Description,
			DefaultFor:  p.DefaultFor,
		})
	}
	return out
}

func clonePtrFloat(in *float64) *float64 {
	if in == nil {
		return nil
	}
	v := *in
	return &v
}

func clonePtrInt(in *int) *int {
	if in == nil {
		return nil
	}
	v := *in
	return &v
}

// Compile-time witness.
var _ NodesAPI = (*Impl)(nil)
