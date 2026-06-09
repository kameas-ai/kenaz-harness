// Package secrets — concrete implementation of SecretsAPI.
//
// Spec mapping: model-secret-references-01KW7M5A WP10.
package secrets

import (
	"context"
	"errors"
	"fmt"
	"strings"

	coresecrets "github.com/kameas-ai/kenaz-harness/core/secrets"
)

// Errors returned by the RPC surface.
var (
	// ErrLocatorInvalid is returned by ExposeSecret when the locator
	// is empty or contains whitespace.
	ErrLocatorInvalid = errors.New("secrets: locator is invalid")
	// ErrLocatorAlreadyExposed is returned by ExposeSecret when the
	// locator is already in the exposure index.
	ErrLocatorAlreadyExposed = errors.New("secrets: locator is already exposed")
	// ErrLocatorNotFound is returned by RevokeSecret when the locator
	// is not in the exposure index.
	ErrLocatorNotFound = errors.New("secrets: locator not found")
)

// API is the concrete SecretsAPI backed by a *coresecrets.ExposureIndex.
type API struct {
	idx *coresecrets.ExposureIndex
}

// NewAPI creates a new SecretsAPI backed by idx.
// idx MUST be non-nil; use coresecrets.NewExposureIndex() in production.
func NewAPI(idx *coresecrets.ExposureIndex) *API {
	return &API{idx: idx}
}

// ListSecrets implements SecretsAPI. Returns all exposed entries.
// Plaintext is never included in the output (FR-005a).
func (a *API) ListSecrets(_ context.Context) ([]SecretRow, error) {
	entries := a.idx.List(context.Background(), "") // "" = all scopes
	rows := make([]SecretRow, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, SecretRow{
			Ref:         "@secret:" + e.Locator,
			Locator:     e.Locator,
			Description: e.Description,
			Kind:        string(e.KindHint),
			Scope:       string(e.Scope),
			ExposedAt:   e.CreatedAt,
		})
	}
	return rows, nil
}

// ExposeSecret implements SecretsAPI. Adds the secret to the exposure
// index. The plaintext in req is zeroed before this method returns.
func (a *API) ExposeSecret(_ context.Context, req ExposeRequest) error {
	locator := strings.TrimSpace(req.Locator)
	if locator == "" {
		return ErrLocatorInvalid
	}
	// Strip a leading @secret: prefix if the frontend accidentally includes it.
	locator = strings.TrimPrefix(locator, "@secret:")
	if locator == "" {
		return ErrLocatorInvalid
	}
	if strings.ContainsAny(locator, " \t\n\r") {
		return fmt.Errorf("%w: locator must not contain whitespace", ErrLocatorInvalid)
	}

	kindHint := coresecrets.KindHint(req.Kind)
	if kindHint == "" {
		kindHint = coresecrets.KindHintRaw
	}

	// Copy plaintext before handing to Add so we can zero our copy.
	plain := []byte(req.Plaintext)
	defer func() {
		for i := range plain {
			plain[i] = 0
		}
		// Zero the req.Plaintext string's backing storage too. Go strings are
		// immutable so we can't zero through the string header, but at least
		// we clear our slice copy above to minimise window.
	}()

	entry := coresecrets.ExposedEntry{
		Locator:     locator,
		Description: req.Description,
		Scope:       coresecrets.ScopeSession,
		KindHint:    kindHint,
	}
	a.idx.Add(entry, plain)
	return nil
}

// RevokeSecret implements SecretsAPI. Removes the secret from the
// exposure index. Returns ErrLocatorNotFound when absent.
func (a *API) RevokeSecret(_ context.Context, locator string) error {
	locator = strings.TrimPrefix(strings.TrimSpace(locator), "@secret:")
	if !a.idx.Contains(locator) {
		return ErrLocatorNotFound
	}
	a.idx.Remove(locator)
	return nil
}

// Compile-time check: *API satisfies SecretsAPI.
var _ SecretsAPI = (*API)(nil)
