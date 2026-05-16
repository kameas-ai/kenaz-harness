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
	"time"
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
	Role     string `json:"role"`
	// org_settings is opaque for now.
	OrgSettings json.RawMessage `json:"org_settings,omitempty"`

	// Fields the fleet AuthContext has but enroll-response doesn't serialize yet.
	// Zero-values are tolerated; we will coordinate adding them in a follow-up.
	UserID      string `json:"user_id,omitempty"`
	Email       string `json:"email,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Tier        string `json:"tier,omitempty"`
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

	reqURL := c.profile.FleetBaseURL + "/api/v1/enroll"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return Identity{}, fmt.Errorf("fleet: enroll request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+ts.AccessToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Identity{}, fmt.Errorf("fleet: enroll: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusUnauthorized {
		// Attempt token refresh.
		newTS, refreshErr := RefreshTokenSet(ctx, c.profile, ts.RefreshToken)
		if refreshErr != nil {
			return Identity{}, ErrTokenExpired
		}
		if saveErr := SaveTokens(newTS); saveErr != nil {
			return Identity{}, saveErr
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
		return Identity{}, fmt.Errorf("fleet: enroll: status %d: %s", resp.StatusCode, respBody)
	}

	var er enrollResponse
	if err := json.Unmarshal(respBody, &er); err != nil {
		return Identity{}, fmt.Errorf("fleet: parse enroll response: %w", err)
	}

	id := Identity{
		UserID:      er.UserID,
		OrgID:       orgIDToString(er.OrgID),
		TeamID:      er.TeamID,
		OrgName:     er.OrgName,
		TeamName:    er.TeamName,
		Email:       er.Email,
		DisplayName: er.DisplayName,
		Tier:        er.Tier,
		FetchedAt:   time.Now(),
	}
	if er.Role != "" {
		id.Roles = []string{er.Role}
	}

	// Cache to disk.
	if c.dataDir != "" {
		_ = SaveIdentity(c.dataDir, id)
	}
	return id, nil
}
