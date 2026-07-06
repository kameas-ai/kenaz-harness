package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// ErrNotSignedIn is returned when no stored credential exists for a recipe and
// a valid access token is required — the UI should offer the sign-in button.
var ErrNotSignedIn = errors.New("oauth: not signed in")

// StoredCredential is the persisted result of a sign-in: the tokens plus the
// minimum needed to refresh them later (token endpoint + client id) without
// re-running discovery. It is serialized to the secrets backend under a
// per-recipe locator.
//
// SECURITY: this carries bearer + refresh secrets. Persist it only through the
// OS keychain / secrets backend, never plaintext on disk, and redact it from
// logs.
type StoredCredential struct {
	AccessToken   string    `json:"access_token"`
	RefreshToken  string    `json:"refresh_token,omitempty"`
	TokenType     string    `json:"token_type,omitempty"`
	Scope         string    `json:"scope,omitempty"`
	ExpiresAt     time.Time `json:"expires_at,omitempty"`
	TokenEndpoint string    `json:"token_endpoint"`
	ClientID      string    `json:"client_id"`
}

// Marshal serializes the credential to JSON for the secrets backend.
func (c *StoredCredential) Marshal() ([]byte, error) {
	return json.Marshal(c)
}

// UnmarshalCredential parses a credential blob from the secrets backend.
func UnmarshalCredential(blob []byte) (*StoredCredential, error) {
	var c StoredCredential
	if err := json.Unmarshal(blob, &c); err != nil {
		return nil, fmt.Errorf("oauth: unmarshal credential: %w", err)
	}
	return &c, nil
}

// expired reports whether the access token is at/past expiry (with skew).
func (c *StoredCredential) expired(now time.Time) bool {
	if c.ExpiresAt.IsZero() {
		return false
	}
	return !now.Before(c.ExpiresAt.Add(-30 * time.Second))
}

// EnsureValid returns a credential whose access token is currently valid,
// refreshing in place when it has expired and a refresh token is available.
// The returned bool reports whether a refresh occurred (so the caller can
// re-persist). When the token is expired and no refresh is possible, it
// returns ErrNotSignedIn so the caller re-runs interactive sign-in.
func EnsureValid(ctx context.Context, client *http.Client, c *StoredCredential, now time.Time) (*StoredCredential, bool, error) {
	if c == nil || c.AccessToken == "" {
		return nil, false, ErrNotSignedIn
	}
	if !c.expired(now) {
		return c, false, nil
	}
	if c.RefreshToken == "" || c.TokenEndpoint == "" {
		return nil, false, ErrNotSignedIn
	}
	asm := &AuthServerMetadata{TokenEndpoint: c.TokenEndpoint}
	tok, err := Refresh(ctx, client, asm, c.ClientID, c.RefreshToken, now)
	if err != nil {
		return nil, false, err
	}
	refreshed := &StoredCredential{
		AccessToken:   tok.AccessToken,
		RefreshToken:  tok.RefreshToken,
		TokenType:     tok.TokenType,
		Scope:         tok.Scope,
		ExpiresAt:     tok.ExpiresAt,
		TokenEndpoint: c.TokenEndpoint,
		ClientID:      c.ClientID,
	}
	return refreshed, true, nil
}

// AuthorizationHeader returns the value for the HTTP Authorization header
// ("Bearer <token>"). TokenType defaults to Bearer when unset.
func (c *StoredCredential) AuthorizationHeader() string {
	return "Bearer " + c.AccessToken
}
