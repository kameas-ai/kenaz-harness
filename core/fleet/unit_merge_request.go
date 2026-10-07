// Package fleet — unit_merge_request.go
//
// Promote-as-merge-request (WP16) — the harness half of "write-up-reviewed"
// (DESIGN §4). When a Unit is promoted UP a classification level
// (personal→team→org), the harness MUST NOT write the higher layer directly;
// instead it opens a MERGE REQUEST on fleet via
// POST /api/v1/context/merge-requests. The fleet web UI reviews and accepts
// it (RBAC slots into that review gate later — FR-040, spec §"Out of scope").
//
// This file owns the wire shapes + the thin client. It is fleet-touching and
// lives in core/fleet so core/units stays fleet-free (DIRECTIVE_001). The
// field shapes match the fleet merge_requests object:
//
//	unit_node_id, from_classification, to_classification,
//	proposed_version, title, body, metadata.
//
// (unified-context-artifacts-01NCTXU01 / Phase 3 / WP16)
package fleet

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"context"

	"github.com/kameas-ai/kenaz-harness/core/units"
)

// ErrPromoteNotUp is returned when CreateMergeRequestForPromote is asked to
// promote to a classification that is not strictly higher than the source's
// (personal < team < org). Promotion is the only direction that opens a merge
// request; same-level or downward moves are not "write-up" events.
var ErrPromoteNotUp = errors.New("fleet: promote target is not a higher classification")

// classificationRank orders the classification ladder for the write-up check.
// personal(0) < team(1) < org(2). Unknown values rank -1 and never qualify as
// "up".
func classificationRank(c units.Classification) int {
	switch c {
	case units.ClassPersonal:
		return 0
	case units.ClassTeam:
		return 1
	case units.ClassOrg:
		return 2
	default:
		return -1
	}
}

// IsPromotionUp reports whether moving from→to climbs the classification
// ladder (the write-up-reviewed trigger). personal→team, personal→org and
// team→org are up; everything else (same level, downward, unknown) is not.
func IsPromotionUp(from, to units.Classification) bool {
	rf, rt := classificationRank(from), classificationRank(to)
	if rf < 0 || rt < 0 {
		return false
	}
	return rt > rf
}

// mergeRequestInput is the POST body for /api/v1/context/merge-requests. It
// mirrors kenaz-fleet service/api_types.go MergeRequestCreateRequest field for
// field (unit_node_id, to_classification, proposed_title, proposed_body,
// proposed_metadata). Fleet snapshots from_classification and
// proposed_version from the live node server-side, so neither is sent.
// unit_node_id must be a UUID (fleet uuid.Parse, handlers_unit_merge.go) —
// the unit's wire id, never its local ULID (WP01).
//
// Before WP01 this sent title/body/metadata/from_classification/
// proposed_version: fleet silently ignored all five (unknown JSON fields),
// so the proposal always carried the live node's content.
type mergeRequestInput struct {
	UnitNodeID       string          `json:"unit_node_id"`
	ToClassification string          `json:"to_classification"`
	ProposedTitle    string          `json:"proposed_title,omitempty"`
	ProposedBody     string          `json:"proposed_body,omitempty"`
	ProposedMetadata json.RawMessage `json:"proposed_metadata,omitempty"`
}

// mergeRequestResponse is fleet's MergeRequestResponse envelope
// ({"merge_request": {...}, "node": {...}?}).
type mergeRequestResponse struct {
	MergeRequest MergeRequest `json:"merge_request"`
}

// MergeRequest is the harness-side view of a fleet merge_requests row returned
// by the create endpoint (fleet api_types.go MergeRequest). Status is
// server-assigned (typically "open").
type MergeRequest struct {
	ID                 string          `json:"id"`
	UnitNodeID         string          `json:"unit_node_id"`
	FromClassification string          `json:"from_classification"`
	ToClassification   string          `json:"to_classification"`
	ProposedVersion    int             `json:"proposed_version"`
	Title              string          `json:"proposed_title"`
	Body               string          `json:"proposed_body"`
	Metadata           json.RawMessage `json:"proposed_metadata,omitempty"`
	Status             string          `json:"status"`
	CreatedAt          string          `json:"created_at"`
}

// CreateMergeRequestForPromote opens a merge request that proposes promoting
// src UP to toClass. It is the harness side of "promote = merge request, not a
// silent write" (FR-040). The source unit is left UNTOUCHED — the higher layer
// only changes if a reviewer accepts the request on fleet.
//
// Behaviour:
//   - toClass must be strictly higher than src.Classification, else
//     ErrPromoteNotUp. (Personal→team→org only; the personal source itself is
//     never pushed — only the proposal travels.)
//   - When fleet is disabled / signed-out / unentitled, returns
//     (nil, ErrFleetDisabled | ErrNotSignedIn | ErrCapabilityNotInTier) so the
//     caller can degrade to a local-only posture without crashing. A server
//     refusal is a *ContextPushError mapped by its code (404 node_not_found
//     unless owner / team member, 403 not_team_member, capability_not_in_tier,
//     …; a bare 403 is ErrContextPushForbidden) — context_push_errors.go. Personal
//     units never leave the machine on their own; only an explicit, reviewed
//     proposal does (and even that carries just the proposed body, gated by the
//     team-graph capability).
//
// The from/to classifications are sent as the fleet sync vocabulary
// (team_shared/org_shared) when they map, falling back to the raw unit
// classification string for the personal source (which has no sync class).
func (s *UnitSyncer) CreateMergeRequestForPromote(ctx context.Context, src units.Unit, toClass units.Classification, title, body string) (*MergeRequest, error) {
	if !IsPromotionUp(src.Classification, toClass) {
		return nil, fmt.Errorf("%w: %s→%s", ErrPromoteNotUp, src.Classification, toClass)
	}
	// Review F2: a merge request is ONLY team→org. Fleet's MR targets an
	// EXISTING node and rejects a target equal to its current
	// classification, so personal→team is a straight push (PushUnit), and
	// personal→org is push-at-team then this MR. Callers orchestrate that
	// (fleet view Unit_PromoteAsMergeRequest).
	if src.Classification != units.ClassTeam || toClass != units.ClassOrg {
		return nil, fmt.Errorf("%w: merge requests are team→org only (got %s→%s; personal→team is a direct push)", ErrPromoteNotUp, src.Classification, toClass)
	}
	if err := s.canSync(); err != nil {
		return nil, err
	}

	in := mergeRequestInput{
		UnitNodeID:       s.WireNodeID(ctx, src.ID),
		ToClassification: mergeRequestClassString(toClass),
		ProposedTitle:    title,
		ProposedBody:     body,
		ProposedMetadata: normaliseMergeMeta(src.Metadata),
	}
	if in.ProposedTitle == "" {
		in.ProposedTitle = src.Title
	}
	if in.ProposedBody == "" {
		in.ProposedBody = src.Body
	}

	resp, err := s.client.PostJSON(ctx, "/api/v1/context/merge-requests", in)
	if err != nil {
		return nil, fmt.Errorf("fleet: create merge request: %w", err)
	}
	defer drain(resp)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		// Map by the envelope's code (PR #173 adds 404 node_not_found and
		// distinct 403 codes); a bare 403 stays a generic refusal.
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return nil, parseContextPushError("merge request", resp.StatusCode, errBody)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("fleet: create merge request read: %w", err)
	}
	var env mergeRequestResponse
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("fleet: create merge request parse: %w", err)
	}
	mr := env.MergeRequest
	return &mr, nil
}

// mergeRequestClassString renders a units.Classification for the merge-request
// wire. team/org map to the fleet sync vocabulary (team_shared/org_shared) so
// the server speaks one classification language; personal (the source of a
// personal→* promotion) has no sync class and is sent verbatim.
func mergeRequestClassString(c units.Classification) string {
	if fc, ok := ClassificationForUnit(c); ok {
		return string(fc)
	}
	return string(c)
}

// normaliseMergeMeta returns nil for an empty/"{}" metadata so the omitempty
// tag drops the field, else passes the JSON through.
func normaliseMergeMeta(m json.RawMessage) json.RawMessage {
	if len(m) == 0 {
		return nil
	}
	if string(m) == "{}" {
		return nil
	}
	return m
}
