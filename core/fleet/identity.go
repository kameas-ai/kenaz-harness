package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// Identity holds the harness user's fleet identity. Fields are populated
// from the fleet enroll-response and cached locally.
//
// OrgID is a string even though fleet currently emits an integer — the
// conversion happens at the boundary via strconv.Itoa for forward-compat.
//
// Tier, Email, and DisplayName may be empty on early fleet versions that
// don't yet serialize them; callers must tolerate zero-values.
type Identity struct {
	UserID      string    `json:"user_id"`
	OrgID       string    `json:"org_id"`
	TeamID      string    `json:"team_id"`
	Email       string    `json:"email,omitempty"`
	DisplayName string    `json:"display_name,omitempty"`
	Tier        string    `json:"tier,omitempty"`
	OrgName     string    `json:"org_name,omitempty"`
	TeamName    string    `json:"team_name,omitempty"`
	Roles       []string  `json:"roles,omitempty"`
	FetchedAt   time.Time `json:"fetched_at"`
}

// identityFilePath returns the cache file path for the identity.
func identityFilePath(dataDir string) string {
	return filepath.Join(dataDir, "fleet", "identity.json")
}

// IdentityFilePath returns the exported cache file path for the identity.
// Used by callers that need to delete the file during sign-out.
func IdentityFilePath(dataDir string) string {
	return identityFilePath(dataDir)
}

// LoadIdentity reads the cached identity from disk. Returns an error when
// the file does not exist or cannot be parsed.
func LoadIdentity(dataDir string) (Identity, error) {
	path := identityFilePath(dataDir)
	data, err := os.ReadFile(path)
	if err != nil {
		return Identity{}, fmt.Errorf("fleet: load identity: %w", err)
	}
	var id Identity
	if err := json.Unmarshal(data, &id); err != nil {
		return Identity{}, fmt.Errorf("fleet: parse identity: %w", err)
	}
	return id, nil
}

// SaveIdentity atomically writes the identity to disk. Uses a tmp+rename
// pattern to prevent partial writes.
func SaveIdentity(dataDir string, id Identity) error {
	dir := filepath.Join(dataDir, "fleet")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("fleet: mkdir fleet: %w", err)
	}
	path := identityFilePath(dataDir)
	data, err := json.Marshal(id)
	if err != nil {
		return fmt.Errorf("fleet: marshal identity: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("fleet: write identity tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("fleet: rename identity: %w", err)
	}
	return nil
}

// clearIdentityFile removes the cached identity file. Best-effort; used
// during sign-out.
func clearIdentityFile(dataDir string) error {
	return os.Remove(identityFilePath(dataDir))
}

// enrollRequest is the JSON body for POST /api/v1/enroll.
type enrollRequest struct {
	NodeID   string `json:"node_id"`
	Platform string `json:"platform"`
	Version  string `json:"version"`
}

// enrollResponse is the JSON shape returned by the fleet enroll endpoint.
// Fields that fleet doesn't yet serialize are tolerated as zero-values.
type enrollResponse struct {
	// Numeric org_id — fleet currently emits an integer.
	OrgID    any    `json:"org_id"` // int or string
	TeamID   string `json:"team_id"`
	OrgName  string `json:"org_name"`
	TeamName string `json:"team_name"`
	// Role is always present on the wire and is exactly one of
	// org_owner | org_admin | org_member (verified against kenaz-fleet
	// main, answering kitty-specs/fleet-session-truth-01DOGF0A/research/
	// fleet-brief.md). Fleet has no plural `roles` array; the Roles field
	// below is tolerated only so a hypothetical future plural form would
	// merge in, and it is never populated by the current server.
	Role  string   `json:"role"`
	Roles []string `json:"roles,omitempty"`
	// org_settings is opaque for now.
	OrgSettings json.RawMessage `json:"org_settings,omitempty"`

	UserID string `json:"user_id,omitempty"`
	Tier   string `json:"tier,omitempty"`

	// UserEmail / UserDisplayName are the keys the fleet enroll response
	// actually serializes (both omitempty — either may be absent). Before
	// this was fixed the harness read only `email`/`display_name`, which
	// are the GET /api/v1/me keys, so every enrolled identity had a blank
	// name and email and the account menu fell back to the org name.
	UserEmail       string `json:"user_email,omitempty"`
	UserDisplayName string `json:"user_display_name,omitempty"`
	// Email / DisplayName are the legacy (/me-shaped) keys, tolerated as a
	// fallback only; the enroll endpoint does not emit them.
	Email       string `json:"email,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}

// email returns the enroll email, preferring the real wire key.
func (er enrollResponse) email() string {
	if v := strings.TrimSpace(er.UserEmail); v != "" {
		return v
	}
	return strings.TrimSpace(er.Email)
}

// displayName returns the enroll display name, preferring the real wire key.
func (er enrollResponse) displayName() string {
	if v := strings.TrimSpace(er.UserDisplayName); v != "" {
		return v
	}
	return strings.TrimSpace(er.DisplayName)
}

// enrollErrorEnvelope matches the Fleet API error response shape for
// POST /api/v1/enroll. Mirrors siteErrorEnvelope in sites.go — this file
// was the odd one out in not parsing the {code, message} envelope that
// the rest of the fleet client already understands.
//
// The envelope may also carry a "details" object (e.g. zitadel_user_id
// on user_not_provisioned); that is real identity data and deliberately
// not unmarshalled here — enrollIdentity only ever needs Code + Message.
type enrollErrorEnvelope struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// orgIDToString converts the fleet org_id (which may be a JSON number or string)
// to a string for forward-compat.
func orgIDToString(v any) string {
	if v == nil {
		return ""
	}
	switch vt := v.(type) {
	case float64:
		return strconv.Itoa(int(vt))
	case string:
		return vt
	case int:
		return strconv.Itoa(vt)
	case json.Number:
		return string(vt)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// enrollIdentity calls POST /api/v1/enroll on the fleet server and returns
// the parsed Identity.
func (c *Client) enrollIdentity(ctx context.Context, nodeID, platform, version string) (Identity, error) {
	ts, err := LoadTokens()
	if err != nil {
		logging.L().Warn("fleet.enroll.load_tokens_failed", "err", err.Error())
		return Identity{}, ErrNotSignedIn
	}

	reqBody := enrollRequest{
		NodeID:   nodeID,
		Platform: platform,
		Version:  version,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return Identity{}, fmt.Errorf("fleet: marshal enroll request: %w", err)
	}

	reqURL, urlErr := c.APIURL(ctx, "/api/v1/enroll")
	if urlErr != nil {
		logging.L().Error("fleet.enroll.api_url_failed", "err", urlErr.Error())
		return Identity{}, urlErr
	}
	logging.L().Info("fleet.enroll.http.start",
		"url", reqURL,
		"body_bytes", len(bodyBytes),
		"access_token_len", len(ts.AccessToken),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(bodyBytes))
	if err != nil {
		logging.L().Error("fleet.enroll.build_request_failed", "err", err.Error())
		return Identity{}, fmt.Errorf("fleet: enroll request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+ts.AccessToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		logging.L().Error("fleet.enroll.transport_error", "err", err.Error(), "url", reqURL)
		// Map common transport failures to a clearer sentinel.
		es := err.Error()
		if strings.Contains(es, "no such host") ||
			strings.Contains(es, "connection refused") ||
			strings.Contains(es, "i/o timeout") ||
			strings.Contains(es, "EOF") {
			return Identity{}, fmt.Errorf("%w (%s): %v", ErrFleetUnreachable, reqURL, err)
		}
		return Identity{}, fmt.Errorf("fleet: enroll: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	contentType := resp.Header.Get("Content-Type")
	logging.L().Info("fleet.enroll.http.response",
		"status", resp.StatusCode,
		"body_bytes", len(respBody),
		"content_type", contentType,
	)

	// CloudFront / ALB / nginx commonly fall through to a dashboard SPA's
	// index.html when /api/v1/* isn't routed to the backend. The HTTP
	// status will be 200 OK, the body will be HTML, and unmarshalling
	// will hit a misleading "invalid character '<'" error. Catch that
	// upstream so the message is actionable for the fleet ops team.
	if strings.HasPrefix(contentType, "text/html") ||
		bytes.HasPrefix(bytes.TrimSpace(respBody), []byte("<")) {
		preview := string(respBody)
		if len(preview) > 200 {
			preview = preview[:200] + "…(truncated)"
		}
		logging.L().Error("fleet.enroll.html_response",
			"status", resp.StatusCode,
			"url", reqURL,
			"content_type", contentType,
			"body_prefix", preview,
		)
		return Identity{}, fmt.Errorf("%w (got %d bytes of HTML from %s)", ErrFleetAPINotRouted, len(respBody), reqURL)
	}

	if resp.StatusCode == http.StatusUnauthorized {
		var newTS TokenSet
		if _, ok := externalTokens(); ok {
			// Renewal is externally owned (host auth broker); re-read the
			// source once and retry. An unchanged token means the session
			// is dead host-side — same contract as http.go's doRefresh.
			logging.L().Info("fleet.enroll.401_rereading_brokered_token")
			reloaded, reloadErr := LoadTokens()
			if reloadErr != nil || reloaded.AccessToken == ts.AccessToken {
				return Identity{}, ErrTokenExpired
			}
			newTS = reloaded
		} else {
			logging.L().Info("fleet.enroll.401_refreshing")
			// Attempt token refresh.
			refreshed, refreshErr := RefreshTokenSet(ctx, c.profile, ts.RefreshToken)
			if refreshErr != nil {
				// Only a definite rejection is an expired session; a
				// transport failure is retryable (review F2).
				return Identity{}, c.refreshFailed("enroll refresh", refreshErr)
			}
			if saveErr := SaveTokens(refreshed); saveErr != nil {
				return Identity{}, saveErr
			}
			c.notifyAuthOK()
			newTS = refreshed
		}
		// Retry with new token.
		req2, _ := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(bodyBytes))
		req2.Header.Set("Content-Type", "application/json")
		req2.Header.Set("Authorization", "Bearer "+newTS.AccessToken)
		resp2, err2 := c.httpClient.Do(req2)
		if err2 != nil {
			return Identity{}, fmt.Errorf("fleet: enroll retry: %w", err2)
		}
		defer resp2.Body.Close()
		respBody, _ = io.ReadAll(resp2.Body)
		resp = resp2
	}

	if resp.StatusCode != http.StatusOK {
		preview := string(respBody)
		if len(preview) > 800 {
			preview = preview[:800] + "…(truncated)"
		}
		logging.L().Error("fleet.enroll.non_2xx",
			"status", resp.StatusCode,
			"url", reqURL,
			"body", preview,
		)
		// 404 with a plain-text Go-stdlib "404 page not found" body
		// means we reached the fleet binary and authed successfully,
		// but the deployed version doesn't register /api/v1/enroll.
		// This is a fleet deployment-version mismatch — the kenaz-fleet
		// branch with the enroll handler hasn't shipped to this env yet.
		if resp.StatusCode == http.StatusNotFound &&
			strings.Contains(strings.ToLower(preview), "404 page not found") {
			return Identity{}, fmt.Errorf("fleet: enroll route not registered on the deployed fleet binary at %s — fleet needs to deploy the branch that ships POST /api/v1/enroll (raw response: %s)", reqURL, preview)
		}
		// 403 user_not_provisioned: the identity authenticated with Zitadel
		// but has no matching Fleet account. Parse the {code, message}
		// envelope (mirrors mapSiteError in sites.go) so this terminal
		// condition is a typed sentinel instead of a raw-body string that
		// nothing downstream can branch on.
		if resp.StatusCode == http.StatusForbidden {
			var env enrollErrorEnvelope
			if jsonErr := json.Unmarshal(respBody, &env); jsonErr == nil && env.Code == "user_not_provisioned" {
				msg := env.Message
				if msg == "" {
					msg = preview
				}
				return Identity{}, fmt.Errorf("%w (server: %s)", ErrUserNotProvisioned, msg)
			}
		}
		return Identity{}, fmt.Errorf("fleet: enroll: status %d: %s", resp.StatusCode, respBody)
	}

	var er enrollResponse
	if err := json.Unmarshal(respBody, &er); err != nil {
		logging.L().Error("fleet.enroll.parse_failed", "err", err.Error(), "body_prefix", string(respBody[:min(len(respBody), 200)]))
		return Identity{}, fmt.Errorf("fleet: parse enroll response: %w", err)
	}

	id := Identity{
		UserID:      er.UserID,
		OrgID:       orgIDToString(er.OrgID),
		TeamID:      er.TeamID,
		OrgName:     er.OrgName,
		TeamName:    er.TeamName,
		Email:       er.email(),
		DisplayName: er.displayName(),
		Tier:        er.Tier,
		FetchedAt:   time.Now(),
	}
	id.Roles = mergeRoles(er.Role, er.Roles)

	// Cache to disk.
	if c.dataDir != "" {
		_ = SaveIdentity(c.dataDir, id)
	}
	return id, nil
}

// mergeRoles combines the singular `role` and plural `roles` enroll fields,
// in order, without empties or duplicates. nil when there are none.
func mergeRoles(role string, roles []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range append([]string{role}, roles...) {
		r = strings.TrimSpace(r)
		if r == "" || seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	return out
}
