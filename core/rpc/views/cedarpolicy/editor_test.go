package cedarpolicy_test

// editor_test.go — tests for the cedar-policy-editor-ui-01KQ8TD6 WP01 surface:
// GetPolicy, SavePolicy, DeletePolicy, ValidatePolicy, InstallTemplate.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/context/audit"
	"github.com/sigil-tech/kaneaz-harness/core/policy/cedar"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/cedarpolicy"
)

// ── fake audit emitter ────────────────────────────────────────────────

type fakeEmitter struct {
	mu      sync.Mutex
	emitted []audit.Event
}

func (f *fakeEmitter) Emit(_ context.Context, e audit.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.emitted = append(f.emitted, e)
	return nil
}

func (f *fakeEmitter) snapshot() []audit.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]audit.Event, len(f.emitted))
	copy(out, f.emitted)
	return out
}

// ── helpers ────────────────────────────────────────────────────────────

func newEditorAPI(t *testing.T, dataDir string) (*cedarpolicy.API, *fakeEmitter) {
	t.Helper()
	emitter := &fakeEmitter{}
	api := cedarpolicy.NewAPIWithOptions(nil, dataDir, emitter, true)
	return api, emitter
}

func newEditorAPIWithEngine(t *testing.T, dataDir string) (*cedarpolicy.API, *fakeEmitter) {
	t.Helper()
	e, err := cedar.NewEngine(cedar.Options{
		IncludeEmbedded: true,
		LoadFromDisk:    true,
		DataDir:         dataDir,
	})
	if err != nil {
		t.Fatalf("cedar.NewEngine: %v", err)
	}
	emitter := &fakeEmitter{}
	api := cedarpolicy.NewAPIWithOptions(e, dataDir, emitter, true)
	return api, emitter
}

// ── ValidatePolicy ─────────────────────────────────────────────────────

func TestValidatePolicy_ValidSource(t *testing.T) {
	t.Parallel()
	api, _ := newEditorAPI(t, t.TempDir())
	result, err := api.ValidatePolicy(context.Background(), `permit(principal, action, resource);`)
	if err != nil {
		t.Fatalf("ValidatePolicy: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected OK=true, got errors: %+v", result.Errors)
	}
}

func TestValidatePolicy_InvalidSource(t *testing.T) {
	t.Parallel()
	api, _ := newEditorAPI(t, t.TempDir())
	result, err := api.ValidatePolicy(context.Background(), `this is not valid cedar at all!!!`)
	if err != nil {
		t.Fatalf("ValidatePolicy: %v", err)
	}
	if result.OK {
		t.Fatal("expected OK=false for invalid Cedar")
	}
	if len(result.Errors) == 0 {
		t.Fatal("expected at least one parse error")
	}
}

func TestValidatePolicy_NeverTouchesDisk(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	api, _ := newEditorAPI(t, dir)
	_, _ = api.ValidatePolicy(context.Background(), `not valid cedar`)
	// Confirm no .cedar files were created.
	policyDir := filepath.Join(dir, "policy")
	if _, err := os.Stat(policyDir); !os.IsNotExist(err) {
		t.Fatal("ValidatePolicy must not create the policy directory")
	}
}

// ── GetPolicy ──────────────────────────────────────────────────────────

func TestGetPolicy_EmbeddedDefault(t *testing.T) {
	t.Parallel()
	api, _ := newEditorAPI(t, t.TempDir())
	detail, err := api.GetPolicy(context.Background(), "default_policy.cedar")
	if err != nil {
		t.Fatalf("GetPolicy embedded: %v", err)
	}
	if !detail.ReadOnly {
		t.Fatal("embedded policy should be ReadOnly")
	}
	if !detail.Embedded {
		t.Fatal("embedded policy should have Embedded=true")
	}
	if len(detail.Source) == 0 {
		t.Fatal("source should not be empty for embedded default")
	}
}

func TestGetPolicy_OnDisk(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	api, _ := newEditorAPI(t, dir)
	const name = "my-test-policy.cedar"
	const body = `permit(principal, action == Action::"use_tool", resource);`
	// Write directly to simulate a pre-existing policy.
	pDir := filepath.Join(dir, "policy")
	if err := os.MkdirAll(pDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pDir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("write policy: %v", err)
	}

	detail, err := api.GetPolicy(context.Background(), name)
	if err != nil {
		t.Fatalf("GetPolicy on-disk: %v", err)
	}
	if detail.ReadOnly {
		t.Fatal("on-disk policy should not be ReadOnly")
	}
	if detail.Source != body {
		t.Fatalf("source mismatch: got %q, want %q", detail.Source, body)
	}
}

func TestGetPolicy_InvalidName(t *testing.T) {
	t.Parallel()
	api, _ := newEditorAPI(t, t.TempDir())
	if _, err := api.GetPolicy(context.Background(), "../etc/passwd.cedar"); err == nil {
		t.Fatal("expected error for path-traversal name")
	}
}

// ── SavePolicy ─────────────────────────────────────────────────────────

func TestSavePolicy_ValidSource_WritesFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	api, emitter := newEditorAPI(t, dir)
	const name = "new-policy.cedar"
	const body = `permit(principal, action, resource);`

	result, err := api.SavePolicy(context.Background(), name, body)
	if err != nil {
		t.Fatalf("SavePolicy: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected parse OK, got errors: %+v", result.Errors)
	}
	// File should exist on disk.
	data, err := os.ReadFile(filepath.Join(dir, "policy", name))
	if err != nil {
		t.Fatalf("ReadFile after save: %v", err)
	}
	if string(data) != body {
		t.Fatalf("body mismatch: got %q, want %q", string(data), body)
	}
	// Audit should have been emitted.
	events := emitter.snapshot()
	if len(events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(events))
	}
	if events[0].Kind != audit.KindPolicyFileSaved {
		t.Fatalf("expected %q audit kind, got %q", audit.KindPolicyFileSaved, events[0].Kind)
	}
	// Verify no source body in the audit payload (privacy invariant).
	rawPayload := string(events[0].Payload)
	if containsBodyText(rawPayload, body) {
		t.Fatal("audit payload must not contain the policy source body")
	}
}

func TestSavePolicy_InvalidSource_NoFileCreated(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	api, emitter := newEditorAPI(t, dir)
	const name = "broken.cedar"

	result, err := api.SavePolicy(context.Background(), name, `this is not cedar!!!`)
	if err != nil {
		t.Fatalf("SavePolicy: %v", err)
	}
	if result.OK {
		t.Fatal("expected parse failure")
	}
	if len(result.Errors) == 0 {
		t.Fatal("expected at least one parse error")
	}
	// File must NOT exist.
	if _, statErr := os.Stat(filepath.Join(dir, "policy", name)); !os.IsNotExist(statErr) {
		t.Fatal("file must not be created when parse fails")
	}
	// No audit should have been emitted.
	if events := emitter.snapshot(); len(events) != 0 {
		t.Fatalf("expected no audit on parse failure, got %d", len(events))
	}
}

func TestSavePolicy_ProtectedName_Rejected(t *testing.T) {
	t.Parallel()
	api, _ := newEditorAPI(t, t.TempDir())
	_, err := api.SavePolicy(context.Background(), "default_policy.cedar", `permit(principal,action,resource);`)
	if err == nil {
		t.Fatal("expected error when saving to protected name")
	}
}

func TestSavePolicy_EditorDisabled_ReturnsError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	api := cedarpolicy.NewAPIWithOptions(nil, dir, nil, false /* disabled */)
	_, err := api.SavePolicy(context.Background(), "foo.cedar", `permit(principal,action,resource);`)
	if err == nil {
		t.Fatal("expected ErrPolicyEditorDisabled")
	}
	if !errors.Is(err, cedarpolicy.ErrPolicyEditorDisabled) {
		t.Fatalf("expected ErrPolicyEditorDisabled, got: %v", err)
	}
}

// ── DeletePolicy ───────────────────────────────────────────────────────

func TestDeletePolicy_RemovesFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	api, emitter := newEditorAPIWithEngine(t, dir)
	const name = "delete-me.cedar"
	const body = `permit(principal, action, resource);`
	// First save it.
	if _, err := api.SavePolicy(context.Background(), name, body); err != nil {
		t.Fatalf("SavePolicy: %v", err)
	}
	// Now delete.
	if err := api.DeletePolicy(context.Background(), name); err != nil {
		t.Fatalf("DeletePolicy: %v", err)
	}
	// File should be gone.
	if _, statErr := os.Stat(filepath.Join(dir, "policy", name)); !os.IsNotExist(statErr) {
		t.Fatal("file should be deleted")
	}
	// Audit: 1 save + 1 delete.
	events := emitter.snapshot()
	if len(events) < 2 {
		t.Fatalf("expected at least 2 audit events, got %d", len(events))
	}
	var hasDelete bool
	for _, e := range events {
		if e.Kind == audit.KindPolicyFileDeleted {
			hasDelete = true
		}
	}
	if !hasDelete {
		t.Fatal("expected policy.file_deleted audit event")
	}
}

func TestDeletePolicy_EmbeddedDefault_Rejected(t *testing.T) {
	t.Parallel()
	api, _ := newEditorAPI(t, t.TempDir())
	if err := api.DeletePolicy(context.Background(), "default_policy.cedar"); err == nil {
		t.Fatal("expected error when deleting embedded default")
	}
}

func TestDeletePolicy_NonExistent_ReturnsError(t *testing.T) {
	t.Parallel()
	api, _ := newEditorAPI(t, t.TempDir())
	if err := api.DeletePolicy(context.Background(), "nonexistent.cedar"); err == nil {
		t.Fatal("expected error for non-existent file")
	}
}

// ── InstallTemplate ────────────────────────────────────────────────────

func TestInstallTemplate_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	api, emitter := newEditorAPIWithEngine(t, dir)
	const template = "filesystem-full-recommended.cedar"
	const dest = "filesystem-full-recommended.cedar"

	detail, err := api.InstallTemplate(context.Background(), template, dest)
	if err != nil {
		t.Fatalf("InstallTemplate: %v", err)
	}
	if detail.Name != dest {
		t.Fatalf("detail.Name = %q, want %q", detail.Name, dest)
	}
	// File should exist.
	if _, statErr := os.Stat(filepath.Join(dir, "policy", dest)); statErr != nil {
		t.Fatalf("installed file should exist: %v", statErr)
	}
	// Audit emitted.
	events := emitter.snapshot()
	if len(events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(events))
	}
	if events[0].Kind != audit.KindPolicyTemplateInstalled {
		t.Fatalf("expected %q, got %q", audit.KindPolicyTemplateInstalled, events[0].Kind)
	}
}

func TestInstallTemplate_DestAlreadyExists_ReturnsErrPolicyExists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	api, _ := newEditorAPI(t, dir)
	const template = "filesystem-full-recommended.cedar"
	const dest = "filesystem-full-recommended.cedar"
	// First install.
	if _, err := api.InstallTemplate(context.Background(), template, dest); err != nil {
		t.Fatalf("first InstallTemplate: %v", err)
	}
	// Second install should return ErrPolicyExists.
	_, err := api.InstallTemplate(context.Background(), template, dest)
	if err == nil {
		t.Fatal("expected ErrPolicyExists on second install")
	}
	if !errors.Is(err, cedarpolicy.ErrPolicyExists) {
		t.Fatalf("expected ErrPolicyExists, got: %v", err)
	}
}

func TestInstallTemplate_UnknownTemplate_ReturnsError(t *testing.T) {
	t.Parallel()
	api, _ := newEditorAPI(t, t.TempDir())
	_, err := api.InstallTemplate(context.Background(), "nonexistent-template.cedar", "dest.cedar")
	if err == nil {
		t.Fatal("expected error for unknown template")
	}
}

func TestInstallTemplate_ProtectedDestName_Rejected(t *testing.T) {
	t.Parallel()
	api, _ := newEditorAPI(t, t.TempDir())
	_, err := api.InstallTemplate(context.Background(), "filesystem-full-recommended.cedar", "default_policy.cedar")
	if err == nil {
		t.Fatal("expected error for protected destination name")
	}
}

// ── privacy invariant ──────────────────────────────────────────────────

// TestNoSourceInAuditPayload confirms the privacy invariant: policy source
// bodies are never recorded in audit events.
func TestNoSourceInAuditPayload(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	api, emitter := newEditorAPI(t, dir)
	const name = "privacy-check.cedar"
	const body = `permit(principal == User::"secret_user", action, resource);`

	result, err := api.SavePolicy(context.Background(), name, body)
	if err != nil {
		t.Fatalf("SavePolicy: %v", err)
	}
	if !result.OK {
		t.Fatalf("SavePolicy parse should succeed, errors: %v", result.Errors)
	}

	events := emitter.snapshot()
	for _, e := range events {
		raw := string(e.Payload)
		if containsBodyText(raw, body) {
			t.Fatalf("audit payload contains policy source body! kind=%q payload=%s", e.Kind, raw)
		}
		// Check for the "secret" part specifically
		if containsBodyText(raw, "secret_user") {
			t.Fatalf("audit payload contains part of policy source! kind=%q payload=%s", e.Kind, raw)
		}
	}
}

// containsBodyText returns true when payload contains a meaningful substring
// of the policy source body. We check for any fragment of ≤20 chars from the middle.
func containsBodyText(payload, body string) bool {
	if len(body) == 0 {
		return false
	}
	// Extract a substring of up to 20 chars from the middle of body.
	mid := len(body) / 2
	end := mid + 20
	if end > len(body) {
		end = len(body)
	}
	if mid >= end {
		return false
	}
	substr := body[mid:end]
	return len(substr) > 0 && payload != "" && payloadContains(payload, substr)
}

func payloadContains(payload, sub string) bool {
	return len(payload) >= len(sub) && findSubstring(payload, sub)
}

func findSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestAuditEmission_Timestamps confirms emitted events have a recent timestamp.
func TestAuditEmission_Timestamps(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	api, emitter := newEditorAPI(t, dir)
	before := time.Now().Add(-time.Second)
	if _, err := api.SavePolicy(context.Background(), "ts-check.cedar", `permit(principal,action,resource);`); err != nil {
		t.Fatalf("SavePolicy: %v", err)
	}
	after := time.Now().Add(time.Second)
	events := emitter.snapshot()
	for _, e := range events {
		if e.TS.Before(before) || e.TS.After(after) {
			t.Fatalf("event timestamp %v out of expected range [%v, %v]", e.TS, before, after)
		}
	}
}
