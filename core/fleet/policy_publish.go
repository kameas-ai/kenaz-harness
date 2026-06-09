// policy_publish.go — Team Cedar policy publish (WP07).
//
// Publishes a Cedar rule to the fleet server via
// POST /api/v1/cedar-policy/publish. Fleet redistributes it to all
// org members via the existing config-pull pipeline (FR-204).
//
// This endpoint is DISTINCT from the existing PUT /api/v1/policy (routing
// policy / provider allowlist) — see docs/fleet-coordination.md for the
// disambiguation note.
//
// Fork-removal recipe: delete this file + Cedar_PublishToTeam binding +
// the "Publish to team" button on CedarEditor.vue.
package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
)

// cedarPublishRequest is the JSON body for POST /api/v1/cedar-policy/publish.
type cedarPublishRequest struct {
	RuleID     string `json:"rule_id"`
	RuleSource string `json:"rule_source"`
}

// cedarPublishResponse is the JSON shape returned by the publish endpoint.
type cedarPublishResponse struct {
	PublishedAt time.Time `json:"published_at,omitempty"`
	BundleID    int64     `json:"bundle_id,omitempty"`
}

// AuditEmitter is the minimal interface the policy publish path needs to
// emit audit events. Callers pass core/rpc/views/audit.API or nil.
type AuditEmitter interface {
	EmitFleetEvent(ctx context.Context, kind contextaudit.Kind, payload any) error
}

// PublishCedarRule publishes ruleSource to the team via POST
// /api/v1/cedar-policy/publish. On success it emits a
// KindFleetPolicyPublished audit event.
//
// Admin gate: the caller must ensure the current Identity has the
// policy_admin role. The server enforces this server-side; if the user
// lacks the role the server returns 403 and this function returns
// ErrCapabilityNotInTier.
//
// authorIdentity is used only for the audit payload (never sent to fleet);
// pass an empty string if the identity is not available.
func (c *Client) PublishCedarRule(
	ctx context.Context,
	ruleID string,
	ruleSource string,
	authorIdentity string,
	emitter AuditEmitter,
) error {
	if c == nil || c.isNop {
		return ErrFleetDisabled
	}

	req := cedarPublishRequest{
		RuleID:     ruleID,
		RuleSource: ruleSource,
	}
	resp, err := c.PostJSON(ctx, "/api/v1/cedar-policy/publish", req)
	if err != nil {
		return fmt.Errorf("fleet/policy: publish cedar rule: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		return ErrCapabilityNotInTier
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated &&
		resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("fleet/policy: publish cedar rule: status %d: %s", resp.StatusCode, body)
	}

	// Drain + discard response body.
	var publishResp cedarPublishResponse
	_ = json.NewDecoder(resp.Body).Decode(&publishResp)

	// Emit audit event (FR-203).
	if emitter != nil {
		payload := contextaudit.FleetPolicyPublishedPayload{
			RuleID: ruleID,
			Author: authorIdentity,
		}
		_ = emitter.EmitFleetEvent(ctx, contextaudit.KindFleetPolicyPublished, payload)
	}
	return nil
}
