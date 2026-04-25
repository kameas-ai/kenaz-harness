// Concrete LLMConnectorAPI implementation. Backs the personal-providers
// flow exposed through the Wails bindings.
//
// The impl owns three collaborators:
//
//   - personal.Store — the JSON-file-backed user-scoped provider store.
//   - bundleProviders — read-only snapshot of bundle-derived profiles.
//     The connector registry's loaded bundle list is the canonical source;
//     the rpc layer caches a snapshot at construction.
//   - keychainWriter — writes a plaintext API key to the OS keychain and
//     returns the indirect reference that ends up in providers.json. In
//     production this is the secrets-keychain backend; tests substitute a
//     fake.
package llm

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	connectorllm "github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/llm/personal"
)

// ProviderProber performs the lightweight verification call used by
// TestProvider. The default impl in this package issues a 1-token
// completion through the connector registry; tests replace it with a
// deterministic fake.
type ProviderProber interface {
	Probe(ctx context.Context, profile connectorllm.ProviderProfile) ProberResult
}

// ProberResult is the prober's structured response. The impl maps it to
// a TestResult.
type ProberResult struct {
	Success   bool
	LatencyMS int
	Message   string
}

// KeychainWriter writes a plaintext credential to the OS keychain under
// locator. The indirect reference returned by Write is what ends up in
// providers.json. Implementations MUST zeroize the supplied byte slice
// before returning.
type KeychainWriter interface {
	Write(ctx context.Context, locator string, plaintext []byte) error
}

// BundleSource exposes the registry's bundle-derived profiles to the
// rpc layer. A nil BundleSource is treated as an empty snapshot.
type BundleSource interface {
	BundleProfiles() []connectorllm.ProviderProfile
}

// Impl is the concrete LLMConnectorAPI.
type Impl struct {
	store    personal.Store
	bundles  BundleSource
	keychain KeychainWriter
	prober   ProviderProber

	mu        sync.Mutex
	validated map[string]bool
}

// Config holds the dependencies the rpc impl needs.
type Config struct {
	Store    personal.Store
	Bundles  BundleSource
	Keychain KeychainWriter
	Prober   ProviderProber
}

// NewImpl assembles an Impl. A nil Store causes AddProvider/RemoveProvider
// to return ErrPersonalStoreUnavailable.
func NewImpl(cfg Config) *Impl {
	return &Impl{
		store:     cfg.Store,
		bundles:   cfg.Bundles,
		keychain:  cfg.Keychain,
		prober:    cfg.Prober,
		validated: map[string]bool{},
	}
}

// ErrPersonalStoreUnavailable is returned by AddProvider/RemoveProvider
// when the rpc impl was built without a backing store. The chassis falls
// back to this when the personal store cannot be created (e.g.
// UserConfigDir errors).
var ErrPersonalStoreUnavailable = errors.New("llm: personal provider store unavailable")

// ErrBundleProviderImmutable is returned by RemoveProvider when the
// caller targets a bundle-derived profile.
var ErrBundleProviderImmutable = errors.New("llm: bundle providers are read-only")

// ListProviders returns the merged bundle + personal list. Bundle entries
// win on ID collision; personal-only entries surface the user's
// keychain-backed providers.
func (i *Impl) ListProviders(_ context.Context) ([]Provider, error) {
	seen := map[string]Provider{}
	if i.bundles != nil {
		for _, p := range i.bundles.BundleProfiles() {
			seen[p.ID] = profileToProvider(p, "bundle", i.isValidated(p.ID))
		}
	}
	if i.store != nil {
		personalList, err := i.store.List()
		if err != nil {
			return nil, fmt.Errorf("llm: list personal providers: %w", err)
		}
		for _, p := range personalList {
			if _, exists := seen[p.ID]; exists {
				continue
			}
			seen[p.ID] = profileToProvider(p, "personal", i.isValidated(p.ID))
		}
	}
	out := make([]Provider, 0, len(seen))
	for _, v := range seen {
		out = append(out, v)
	}
	// Sort for deterministic frontend rendering.
	sortProviders(out)
	return out, nil
}

// StartStream / StopStream are scaffolded by the connector mission and
// remain unwired in the providers-ui mission. Returning a typed error
// keeps the surface honest until that mission lands its real impl.
func (i *Impl) StartStream(_ context.Context, _ string) (string, error) {
	return "", errors.New("llm: streaming not wired in providers-ui mission")
}

// StopStream is the symmetric stub.
func (i *Impl) StopStream(_ context.Context, _ string) error {
	return errors.New("llm: streaming not wired in providers-ui mission")
}

// AddProvider validates the input, writes the plaintext API key (if any)
// to the keychain under the supplied locator, then persists the
// CredentialReference to the personal store.
func (i *Impl) AddProvider(ctx context.Context, in AddProviderInput) error {
	if i.store == nil {
		return ErrPersonalStoreUnavailable
	}
	if in.Cred.Kind != "keychain" {
		return fmt.Errorf("llm: AddProvider requires kind=keychain credential, got %q", in.Cred.Kind)
	}
	if in.PlaintextAPIKey != "" {
		if i.keychain == nil {
			return errors.New("llm: no keychain writer configured; cannot store plaintext key")
		}
		buf := []byte(in.PlaintextAPIKey)
		// Zeroize the input field before any further processing so
		// nothing downstream observes the plaintext.
		in.PlaintextAPIKey = ""
		err := i.keychain.Write(ctx, in.Cred.Locator, buf)
		zeroBytes(buf)
		if err != nil {
			return fmt.Errorf("llm: keychain write %q: %w", in.Cred.Locator, err)
		}
	}
	profile := connectorllm.ProviderProfile{
		ID:     in.ID,
		Kind:   in.Kind,
		Model:  in.Model,
		Region: in.Region,
		Cred: connectorllm.CredentialReference{
			Kind:    in.Cred.Kind,
			Locator: in.Cred.Locator,
		},
	}
	if err := i.store.Add(profile); err != nil {
		return err
	}
	i.mu.Lock()
	delete(i.validated, in.ID)
	i.mu.Unlock()
	return nil
}

// RemoveProvider deletes a personal provider by ID. Bundle-derived
// profiles are rejected so the UI cannot mutate them through this seam.
func (i *Impl) RemoveProvider(_ context.Context, id string) error {
	if i.store == nil {
		return ErrPersonalStoreUnavailable
	}
	if i.bundles != nil {
		for _, p := range i.bundles.BundleProfiles() {
			if p.ID == id {
				return fmt.Errorf("%w: %q", ErrBundleProviderImmutable, id)
			}
		}
	}
	if err := i.store.Remove(id); err != nil {
		return err
	}
	i.mu.Lock()
	delete(i.validated, id)
	i.mu.Unlock()
	return nil
}

// TestProvider runs the configured prober against the named profile and
// returns a TestResult. The validated cache is updated on success so
// subsequent ListProviders calls render an accurate status pill.
func (i *Impl) TestProvider(ctx context.Context, id string) (TestResult, error) {
	profile, err := i.lookupProfile(id)
	if err != nil {
		return TestResult{}, err
	}
	if i.prober == nil {
		return TestResult{
			Success: false,
			Message: "no provider prober configured",
		}, nil
	}
	t0 := time.Now()
	res := i.prober.Probe(ctx, profile)
	if res.LatencyMS == 0 {
		res.LatencyMS = int(time.Since(t0).Milliseconds())
	}
	i.mu.Lock()
	i.validated[id] = res.Success
	i.mu.Unlock()
	return TestResult{
		Success:   res.Success,
		LatencyMS: res.LatencyMS,
		Message:   res.Message,
	}, nil
}

func (i *Impl) lookupProfile(id string) (connectorllm.ProviderProfile, error) {
	if i.bundles != nil {
		for _, p := range i.bundles.BundleProfiles() {
			if p.ID == id {
				return p, nil
			}
		}
	}
	if i.store != nil {
		p, err := i.store.Get(id)
		if err == nil {
			return p, nil
		}
	}
	return connectorllm.ProviderProfile{}, fmt.Errorf("llm: provider %q not found", id)
}

func (i *Impl) isValidated(id string) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.validated[id]
}

// profileToProvider lifts a connector ProviderProfile into the rpc-side
// Provider DTO. Name defaults to ID when not specified.
func profileToProvider(p connectorllm.ProviderProfile, source string, validated bool) Provider {
	return Provider{
		ID:        p.ID,
		Name:      p.ID,
		Tier:      source,
		Model:     p.Model,
		Source:    source,
		Validated: validated,
	}
}

// sortProviders orders providers by Source (bundle first) then ID. The
// frontend expects deterministic rendering for table-row keys and for
// snapshot tests.
func sortProviders(ps []Provider) {
	for i := 1; i < len(ps); i++ {
		for j := i; j > 0 && lessProvider(ps[j], ps[j-1]); j-- {
			ps[j], ps[j-1] = ps[j-1], ps[j]
		}
	}
}

func lessProvider(a, b Provider) bool {
	if a.Source != b.Source {
		// "bundle" before "personal" lexicographically.
		return a.Source < b.Source
	}
	return a.ID < b.ID
}

func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// Compile-time witness.
var _ LLMConnectorAPI = (*Impl)(nil)
