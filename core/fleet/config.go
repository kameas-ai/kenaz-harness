package fleet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/logging"
)

// FleetConfig is the runtime configuration the dashboard SPA fetches from
// its own origin's /config.json. The harness fetches the same file to
// learn the API host (api_base_url) — Fleet is a two-hostname SaaS shape
// (Stripe / GitHub / Linear convention): the SPA host serves the UI; a
// separate api.* host serves /api/v1/*. The "fleet URL" the user enters
// is always the SPA host; the API host is discovered at sign-in time.
//
// Only api_base_url is binding for the harness — the issuer / client_id /
// redirect_uri / scopes fields are for the dashboard's OIDC client and
// are ignored here (the harness is a separate Zitadel app `kameas-native`
// with its own ldflag-injected client_id and RFC 8252 loopback redirect).
// We parse them anyway so future cross-checks (e.g. "config issuer
// doesn't match harness's expected issuer") have somewhere to land.
type FleetConfig struct {
	Issuer                string    `json:"issuer,omitempty"`
	ClientID              string    `json:"client_id,omitempty"`
	RedirectURI           string    `json:"redirect_uri,omitempty"`
	PostLogoutRedirectURI string    `json:"post_logout_redirect_uri,omitempty"`
	Scopes                string    `json:"scopes,omitempty"`
	APIBaseURL            string    `json:"api_base_url"`
	FetchedAt             time.Time `json:"fetched_at"`
}

// fleetConfigTTL is how long we trust a cached config before re-fetching.
// 5 minutes mirrors the capability poller cadence — short enough that an
// API-URL change at deployment time propagates quickly without making
// every API call pay the round-trip.
const fleetConfigTTL = 5 * time.Minute

var (
	// In-memory cache keyed by SPA base URL. Survives the process; not
	// shared across restarts (the disk cache covers that).
	configCacheMu sync.RWMutex
	configCache   = map[string]FleetConfig{}
)

// FetchFleetConfig retrieves /config.json from the given SPA base URL.
// On success the returned FleetConfig has FetchedAt set to time.Now.
//
// Failure modes are mapped to typed errors so the AccountPanel can
// render an actionable message:
//   - HTML response (CloudFront fall-through to SPA) → ErrFleetAPINotRouted
//   - Transport failure (DNS, refused, timeout)      → ErrFleetUnreachable
//   - 4xx/5xx                                          → wrapped error w/ status
//   - Missing api_base_url field                       → typed error
func FetchFleetConfig(ctx context.Context, spaBaseURL string) (FleetConfig, error) {
	url := strings.TrimRight(spaBaseURL, "/") + "/config.json"
	logging.L().Info("fleet.config.fetch.start", "url", url)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return FleetConfig{}, fmt.Errorf("fleet: config request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		logging.L().Error("fleet.config.fetch.transport_error", "err", err.Error(), "url", url)
		es := err.Error()
		if strings.Contains(es, "no such host") ||
			strings.Contains(es, "connection refused") ||
			strings.Contains(es, "i/o timeout") ||
			strings.Contains(es, "EOF") {
			return FleetConfig{}, fmt.Errorf("%w (%s): %v", ErrFleetUnreachable, url, err)
		}
		return FleetConfig{}, fmt.Errorf("fleet: config fetch: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	ct := resp.Header.Get("Content-Type")

	logging.L().Info("fleet.config.fetch.response",
		"status", resp.StatusCode,
		"content_type", ct,
		"body_bytes", len(body),
	)

	if resp.StatusCode != http.StatusOK {
		preview := string(body)
		if len(preview) > 400 {
			preview = preview[:400] + "…(truncated)"
		}
		logging.L().Error("fleet.config.fetch.non_2xx",
			"status", resp.StatusCode,
			"url", url,
			"body", preview,
		)
		return FleetConfig{}, fmt.Errorf("fleet: config fetch status %d from %s", resp.StatusCode, url)
	}

	// Detect a CloudFront/S3 SPA-fallback that returns 200 with index.html
	// for /config.json — happens when the static origin doesn't carry
	// config.json. Distinct from the "API endpoint returned HTML"
	// error: this means the SPA host itself isn't fully wired.
	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(ct, "text/html") || strings.HasPrefix(trimmed, "<") {
		logging.L().Error("fleet.config.fetch.html_response",
			"url", url,
			"content_type", ct,
			"body_prefix", truncate(trimmed, 200),
		)
		return FleetConfig{}, fmt.Errorf("%w: /config.json at %s returned HTML — SPA host is not serving the runtime config", ErrFleetAPINotRouted, url)
	}

	var cfg FleetConfig
	if err := json.Unmarshal(body, &cfg); err != nil {
		logging.L().Error("fleet.config.fetch.parse_failed", "err", err.Error(), "body_prefix", truncate(string(body), 200))
		return FleetConfig{}, fmt.Errorf("fleet: parse config.json: %w", err)
	}
	if cfg.APIBaseURL == "" {
		logging.L().Error("fleet.config.fetch.missing_api_base_url", "url", url)
		return FleetConfig{}, errors.New("fleet: config.json missing api_base_url")
	}
	cfg.APIBaseURL = strings.TrimRight(cfg.APIBaseURL, "/")
	cfg.FetchedAt = time.Now().UTC()
	logging.L().Info("fleet.config.fetch.success",
		"api_base_url", cfg.APIBaseURL,
		"issuer", cfg.Issuer,
		"client_id", shortID(cfg.ClientID),
	)
	return cfg, nil
}

// ResolveFleetConfig returns a cached config or fetches a fresh one.
// Cache check order: in-memory → disk → network.
func ResolveFleetConfig(ctx context.Context, dataDir, spaBaseURL string) (FleetConfig, error) {
	if spaBaseURL == "" {
		return FleetConfig{}, errors.New("fleet: spa base URL is empty")
	}
	key := strings.TrimRight(spaBaseURL, "/")

	configCacheMu.RLock()
	if cached, ok := configCache[key]; ok && time.Since(cached.FetchedAt) < fleetConfigTTL {
		configCacheMu.RUnlock()
		return cached, nil
	}
	configCacheMu.RUnlock()

	if dataDir != "" {
		if disk, err := loadFleetConfigFromDisk(dataDir, key); err == nil && time.Since(disk.FetchedAt) < fleetConfigTTL {
			configCacheMu.Lock()
			configCache[key] = disk
			configCacheMu.Unlock()
			return disk, nil
		}
	}

	cfg, err := FetchFleetConfig(ctx, key)
	if err != nil {
		return FleetConfig{}, err
	}

	configCacheMu.Lock()
	configCache[key] = cfg
	configCacheMu.Unlock()

	if dataDir != "" {
		if saveErr := saveFleetConfigToDisk(dataDir, key, cfg); saveErr != nil {
			logging.L().Warn("fleet.config.cache.save_failed", "err", saveErr.Error())
		}
	}
	return cfg, nil
}

// SeedFleetConfigForTesting prepopulates the in-memory cache for the
// given SPA base URL. Test-only helper — production code should never
// call this. Tests use it to avoid having to add a /config.json handler
// to every httptest server.
func SeedFleetConfigForTesting(spaBaseURL string, cfg FleetConfig) {
	key := strings.TrimRight(spaBaseURL, "/")
	if cfg.FetchedAt.IsZero() {
		cfg.FetchedAt = time.Now().UTC()
	}
	configCacheMu.Lock()
	configCache[key] = cfg
	configCacheMu.Unlock()
}

// InvalidateFleetConfig drops the cached config for a given SPA host so
// the next ResolveFleetConfig call re-fetches. Useful after observing a
// config-drift symptom (e.g. unexpected HTML response from an API call).
func InvalidateFleetConfig(dataDir, spaBaseURL string) {
	key := strings.TrimRight(spaBaseURL, "/")
	configCacheMu.Lock()
	delete(configCache, key)
	configCacheMu.Unlock()
	if dataDir != "" {
		_ = os.Remove(fleetConfigDiskPath(dataDir, key))
	}
}

func fleetConfigDiskPath(dataDir, spaBaseURL string) string {
	sum := sha256.Sum256([]byte(spaBaseURL))
	short := hex.EncodeToString(sum[:])[:16]
	return filepath.Join(dataDir, "fleet", "config_"+short+".json")
}

func loadFleetConfigFromDisk(dataDir, spaBaseURL string) (FleetConfig, error) {
	path := fleetConfigDiskPath(dataDir, spaBaseURL)
	b, err := os.ReadFile(path)
	if err != nil {
		return FleetConfig{}, err
	}
	var cfg FleetConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return FleetConfig{}, err
	}
	return cfg, nil
}

func saveFleetConfigToDisk(dataDir, spaBaseURL string, cfg FleetConfig) error {
	path := fleetConfigDiskPath(dataDir, spaBaseURL)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…(truncated)"
}
