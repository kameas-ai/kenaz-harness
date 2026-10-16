package settings

// approvals_local_dismiss.go — "Not now" on an informational hub item is a
// LOCAL memory, never a Fleet record. Legal ruling (kameas-ai-11,
// 2026-10-16): a server-side record of who declined an ML offer is new
// personal data that could be used to pressure members under Member Choice,
// so no client records it. The device remembers a dismissal per item id AND
// version: a revised item (a new version) shows again.
//
// Only informational items whose approve action the harness does not send
// can be dismissed this way (PendingApprovalView.LocalDismiss). Required
// items cannot be dismissed at all.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// dismissedApprovalsFile is <fleetDataDir>/fleet/dismissed_approvals.json:
// {"<item id>": "<item version>"}.
const dismissedApprovalsFile = "dismissed_approvals.json"

var dismissMu sync.Mutex

// ErrNotLocallyDismissible is returned for an item that is required, or
// whose approve action the harness sends: those are never hidden locally.
var ErrNotLocallyDismissible = errors.New("settings: this item can't be dismissed on this device")

func dismissedPath(dataDir string) string {
	return filepath.Join(dataDir, "fleet", dismissedApprovalsFile)
}

func loadDismissed(dataDir string) map[string]string {
	out := map[string]string{}
	if dataDir == "" {
		return out
	}
	raw, err := os.ReadFile(dismissedPath(dataDir))
	if err != nil {
		return out
	}
	_ = json.Unmarshal(raw, &out) // a corrupt file means "nothing dismissed"
	return out
}

func saveDismissed(dataDir string, m map[string]string) error {
	if dataDir == "" {
		return errors.New("settings: no data dir for local dismissals")
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "fleet"), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp := dismissedPath(dataDir) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, dismissedPath(dataDir))
}

// withoutLocallyDismissed drops the items this device dismissed at their
// current version. Required items are never dropped.
func withoutLocallyDismissed(v PendingApprovalsView, dismissed map[string]string) PendingApprovalsView {
	if len(dismissed) == 0 {
		return v
	}
	kept := make([]PendingApprovalView, 0, len(v.Items))
	for _, it := range v.Items {
		if !it.Required && it.LocalDismiss && dismissed[it.ID] == it.Version {
			continue
		}
		kept = append(kept, it)
	}
	v.Items = kept
	return v
}

// FleetDismissItemLocally implements SettingsAPI: hides the informational
// item with id ON THIS DEVICE until its version changes. Nothing is sent to
// Fleet. It answers the fresh list without the item.
func (a *API) FleetDismissItemLocally(ctx context.Context, id string) (PendingApprovalsView, error) {
	c, ok := a.approvalsClient(ctx)
	if !ok {
		return PendingApprovalsView{Items: []PendingApprovalView{}}, ErrApprovalsNotWired
	}
	v, _ := readApprovals(ctx, c)
	var target *PendingApprovalView
	for i := range v.Items {
		if v.Items[i].ID == id {
			target = &v.Items[i]
			break
		}
	}
	if target == nil {
		v.Changed = true
		return a.applyLocalDismissals(v), nil
	}
	if target.Required || !target.LocalDismiss {
		return a.applyLocalDismissals(v), ErrNotLocallyDismissible
	}
	dataDir := a.fleetDataDir()
	dismissMu.Lock()
	m := loadDismissed(dataDir)
	m[target.ID] = target.Version
	err := saveDismissed(dataDir, m)
	dismissMu.Unlock()
	if err != nil {
		return a.applyLocalDismissals(v), err
	}
	return a.applyLocalDismissals(v), nil
}

// applyLocalDismissals filters v with this device's local dismissals.
func (a *API) applyLocalDismissals(v PendingApprovalsView) PendingApprovalsView {
	dismissMu.Lock()
	m := loadDismissed(a.fleetDataDir())
	dismissMu.Unlock()
	return withoutLocallyDismissed(v, m)
}
