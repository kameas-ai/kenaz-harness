package capabilities

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/kameas-ai/kenaz-harness/core/install"
	workflowsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/workflows"
)

// WorkflowConsumer is the slice of the workflows view the workflow provider
// drives. Its List is Workflows_List — the consumer FR-1 names — and every
// install lands through Store.Save (+ cron) via Catalog_Install (builtins)
// or InstallDocument (fleet payloads).
type WorkflowConsumer interface {
	List(ctx context.Context) ([]workflowsview.Summary, error)
	Delete(ctx context.Context, id string) error
	ScheduleClear(ctx context.Context, workflowID string) error
	Catalog_List(ctx context.Context) ([]workflowsview.CatalogEntry, error)
	Catalog_Install(ctx context.Context, id string) (workflowsview.CatalogInstallResult, error)
	Catalog_Update(ctx context.Context, id string) (workflowsview.CatalogInstallResult, error)
	InstallDocument(ctx context.Context, payload []byte, origin workflowsview.DocumentOrigin) (workflowsview.CatalogInstallResult, error)
}

// WorkflowProvider is install.Provider for workflows (WP05): the shipped
// workflow templates (the former Workflows › Catalog) and fleet catalog
// workflow payloads.
type WorkflowProvider struct {
	wf      WorkflowConsumer
	catalog FleetCatalog // nil: builtins only

	mu sync.Mutex
	// fleetWorkflowID maps a fleet catalog id to the workflow id it installs
	// (its slug), learned from every List and every install.
	fleetWorkflowID map[string]string
}

// NewWorkflowProvider wires the workflow provider. cat may be nil.
func NewWorkflowProvider(wf WorkflowConsumer, cat FleetCatalog) *WorkflowProvider {
	return &WorkflowProvider{wf: wf, catalog: cat, fleetWorkflowID: map[string]string{}}
}

var _ install.Provider = (*WorkflowProvider)(nil)

const workflowConsumer = "workflows store"

// Kind implements install.Provider.
func (p *WorkflowProvider) Kind() install.Kind { return install.KindWorkflow }

// List implements install.Provider.
func (p *WorkflowProvider) List(ctx context.Context, _ install.Filter) (install.Listing, error) {
	out := install.Listing{Items: []install.Item{}}
	userWF, err := p.userWorkflows(ctx)
	if err != nil {
		return install.Listing{}, err
	}

	builtins, berr := p.wf.Catalog_List(ctx)
	if berr != nil {
		out.Unavailable = append(out.Unavailable, install.Unavailable{
			Source: install.SourceBuiltin, Reason: "error", Message: berr.Error(),
		})
	}
	sort.Slice(builtins, func(i, j int) bool { return builtins[i].Name < builtins[j].Name })
	for _, e := range builtins {
		it := install.Item{
			Kind: install.KindWorkflow, ID: e.ID, Version: e.Version,
			Name: firstNonEmpty(e.Name, e.ID), Description: e.Description,
			Source: install.SourceBuiltin,
		}
		it.State = workflowState(userWF, e.ID)
		it.State.UpdateAvailable = it.State.Installed && e.InstallStatus == "installed_outdated"
		out.Items = append(out.Items, it)
	}

	if p.catalog != nil {
		entries, err := p.catalog.List(ctx, string(install.KindWorkflow))
		if err != nil {
			out.Unavailable = append(out.Unavailable, fleetUnavailable(p.catalog, err)...)
		}
		newest := newestByID(entries)
		ids := make([]string, 0, len(newest))
		for id := range newest {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			e := newest[id]
			p.remember(e.ID, e.Slug)
			it := install.Item{
				Kind: install.KindWorkflow, ID: e.ID, Version: e.Version,
				Name: firstNonEmpty(e.Slug, e.ID), Description: e.Description,
				Source: catalogSource(e.Visibility),
			}
			applyLifecycle(&it, e)
			it.State = workflowState(userWF, firstNonEmpty(e.Slug, e.ID))
			if s, ok := userWF[firstNonEmpty(e.Slug, e.ID)]; ok && s.OrgManaged {
				// The installed copy is the org's mandate: Framework.Uninstall
				// refuses it up front instead of failing in Delete.
				it.Source = install.SourceOrgCatalog
				it.ReadOnly = true
				it.ReadOnlyReason = "Required by your org"
			}
			out.Items = append(out.Items, it)
		}
	}
	return out, nil
}

// Detail implements install.Provider.
func (p *WorkflowProvider) Detail(ctx context.Context, id string) (install.Item, error) {
	l, err := p.List(ctx, install.Filter{})
	if err != nil {
		return install.Item{}, err
	}
	for _, it := range l.Items {
		if it.ID == id {
			return it, nil
		}
	}
	return install.Item{}, fmt.Errorf("%w: workflow %q", install.ErrNotFound, id)
}

// Requirements implements install.Provider. A workflow installs with no
// input; credentials its mcp_call steps reference are reported by the
// install result (MissingCredentials), not demanded up front.
func (p *WorkflowProvider) Requirements(context.Context, string) ([]install.Requirement, error) {
	return nil, nil
}

// Verify implements install.Provider: a shipped template is part of the
// binary; anything else is a fleet catalog item whose signed payload is
// fetched here, once.
func (p *WorkflowProvider) Verify(ctx context.Context, ref install.Ref) (install.Verification, error) {
	if p.isBuiltin(ctx, ref.ID) {
		return install.Verification{Method: install.VerifyBuiltin}, nil
	}
	if p.catalog == nil {
		return install.Verification{}, fmt.Errorf("%w: workflow %q", install.ErrNotFound, ref.ID)
	}
	if ref.Version == "" {
		return install.Verification{}, fmt.Errorf("install workflow %q: a catalog version is required", ref.ID)
	}
	payload, sig, err := p.catalog.Fetch(ctx, ref.ID, ref.Version)
	if err != nil {
		return install.Verification{}, err
	}
	return install.Verification{Method: install.VerifySignature, Payload: payload, Signature: sig}, nil
}

// Install implements install.Provider. Returns the
// workflowsview.CatalogInstallResult Workflows_CatalogInstall hands back.
//
// A shipped template that is already installed is UPDATED
// (Catalog_Update: new body, the user's schedule state kept); otherwise it
// is installed. A fleet payload goes to InstallDocument with the item's
// advertised id, which refuses — before writing anything — a payload whose
// id is not that slug or that would overwrite a workflow this item does not
// own (review H1/H2).
func (p *WorkflowProvider) Install(ctx context.Context, req install.InstallRequest) (any, error) {
	if req.Verification.Method == install.VerifyBuiltin {
		st, err := p.InstalledState(ctx, req.Ref.ID)
		if err != nil {
			return nil, err
		}
		if st.Installed {
			return p.wf.Catalog_Update(ctx, req.Ref.ID)
		}
		return p.wf.Catalog_Install(ctx, req.Ref.ID)
	}
	slug, err := p.advertisedSlug(ctx, req.Ref.ID)
	if err != nil {
		return nil, err
	}
	res, err := p.wf.InstallDocument(ctx, req.Verification.Payload, workflowsview.DocumentOrigin{
		CatalogID: req.Ref.ID, Slug: slug, Version: req.Ref.Version,
	})
	if err != nil {
		return nil, err
	}
	p.remember(req.Ref.ID, res.WorkflowID)
	return res, nil
}

// advertisedSlug is the workflow id catalogID advertised — learned from a
// List, or fetched now. Without it the payload's id cannot be checked, so
// the install is refused.
func (p *WorkflowProvider) advertisedSlug(ctx context.Context, catalogID string) (string, error) {
	p.mu.Lock()
	slug, ok := p.fleetWorkflowID[catalogID]
	p.mu.Unlock()
	if ok {
		return slug, nil
	}
	if p.catalog == nil {
		return "", fmt.Errorf("%w: workflow %q", install.ErrNotFound, catalogID)
	}
	entries, err := p.catalog.List(ctx, string(install.KindWorkflow))
	if err != nil {
		return "", fmt.Errorf("install workflow %q: cannot confirm the id its catalog item advertises: %w", catalogID, err)
	}
	for _, e := range entries {
		if e.ID == catalogID && e.Slug != "" {
			p.remember(e.ID, e.Slug)
			return e.Slug, nil
		}
	}
	return "", fmt.Errorf("install workflow %q: the catalog item advertises no workflow id", catalogID)
}

// Uninstall implements install.Provider: disarm the schedule, then delete
// the persisted workflow.
func (p *WorkflowProvider) Uninstall(ctx context.Context, id string) error {
	wfID := p.workflowID(id)
	_ = p.wf.ScheduleClear(ctx, wfID) // no schedule is not an error here
	return p.wf.Delete(ctx, wfID)
}

// InstalledState implements install.Provider: Workflows_List lists a
// persisted (user-store) workflow under the id.
func (p *WorkflowProvider) InstalledState(ctx context.Context, id string) (install.State, error) {
	userWF, err := p.userWorkflows(ctx)
	if err != nil {
		return install.State{}, err
	}
	return workflowState(userWF, p.workflowID(id)), nil
}

// Update implements install.Provider. Only a shipped template whose
// installed copy is older than the binary's offers an update; fleet
// workflow payloads carry no installed-version provenance yet (FR-2 brief).
func (p *WorkflowProvider) Update(ctx context.Context, id string) (install.Ref, error) {
	builtins, err := p.wf.Catalog_List(ctx)
	if err != nil {
		return install.Ref{}, err
	}
	for _, e := range builtins {
		if e.ID == id && e.InstallStatus == "installed_outdated" {
			return install.Ref{Kind: install.KindWorkflow, ID: id, Version: e.Version}, nil
		}
	}
	return install.Ref{}, fmt.Errorf("%w: workflow %q", install.ErrNoUpdate, id)
}

func (p *WorkflowProvider) isBuiltin(ctx context.Context, id string) bool {
	builtins, err := p.wf.Catalog_List(ctx)
	if err != nil {
		return false
	}
	for _, e := range builtins {
		if e.ID == id {
			return true
		}
	}
	return false
}

func (p *WorkflowProvider) remember(catalogID, workflowID string) {
	if catalogID == "" || workflowID == "" {
		return
	}
	p.mu.Lock()
	p.fleetWorkflowID[catalogID] = workflowID
	p.mu.Unlock()
}

// workflowID resolves a framework id to the workflow id the store keys on:
// a fleet catalog id maps to its slug; anything else is already one.
func (p *WorkflowProvider) workflowID(id string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if wf, ok := p.fleetWorkflowID[id]; ok {
		return wf
	}
	return id
}

func (p *WorkflowProvider) userWorkflows(ctx context.Context) (map[string]workflowsview.Summary, error) {
	sums, err := p.wf.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]workflowsview.Summary, len(sums))
	for _, s := range sums {
		if s.Source == "user" {
			out[s.ID] = s
		}
	}
	return out, nil
}

func workflowState(userWF map[string]workflowsview.Summary, wfID string) install.State {
	st := install.State{Consumer: workflowConsumer}
	if s, ok := userWF[wfID]; ok {
		st.Installed = true
		st.Version = fmt.Sprintf("v%d", s.Version)
	}
	return st
}
