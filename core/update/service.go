package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/mod/semver"

	"github.com/sigil-tech/kaneaz-harness/core/logging"
)

// Config wires the Service. All required fields are validated by
// NewService; missing required fields produce a hard error at boot
// rather than a silent no-op.
type Config struct {
	// CurrentVersion is the running build's main.Version string.
	// Required. "dev" disables the Available flag (a dev build
	// reports no update; semver-compare against an empty current
	// would otherwise always say "yes").
	CurrentVersion string

	// DataDir is the harness data directory (typically ~/.harness).
	// The Service stages downloads under <DataDir>/update/staging/
	// and writes the Windows pending marker at
	// <DataDir>/update/pending.json.
	DataDir string

	// HTTPClient is used for all manifest + binary fetches. Tests
	// inject a *httptest.Server-rooted client. nil falls back to a
	// 30s-timeout default. Production callers should pass an
	// http.Client with sensible Transport and Timeout settings.
	HTTPClient *http.Client

	// Publisher is the broker-emit surface. nil disables event
	// emission (BackgroundPoll keeps polling but no topic is
	// published). The rpc layer wires its StreamBroker here.
	Publisher Publisher

	// Swapper is the per-platform install primitive. nil falls back
	// to NewRealSwapper(). Tests inject a fake.
	Swapper Swapper

	// RunningBinaryPath overrides the path returned by os.Executable().
	// Tests inject a tmpdir path; production leaves this empty so
	// the Service resolves the running binary at apply time.
	RunningBinaryPath string

	// ManifestURL overrides the channel-derived manifest URL. Tests
	// point this at a httptest.Server. Empty means "use the
	// production URL for the channel".
	ManifestURL string

	// Platform overrides runtime.GOOS+"/"+runtime.GOARCH for tests.
	// Empty means "use the running build's tuple".
	Platform string
}

// Service is the production implementation of update.Service. Wired
// once at boot by the rpc layer; safe for concurrent Check / Download
// calls (no shared mutable state beyond the skipped-versions set).
type service struct {
	cfg     Config
	client  *http.Client
	swapper Swapper

	mu      sync.RWMutex
	skipped map[string]struct{} // version → {}
}

// NewService validates the config and returns a ready Service.
// Required: CurrentVersion, DataDir.
func NewService(cfg Config) (Service, error) {
	if cfg.CurrentVersion == "" {
		return nil, errors.New("update: CurrentVersion required")
	}
	if cfg.DataDir == "" {
		return nil, errors.New("update: DataDir required")
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	swapper := cfg.Swapper
	if swapper == nil {
		swapper = NewRealSwapper()
	}
	s := &service{
		cfg:     cfg,
		client:  client,
		swapper: swapper,
		skipped: make(map[string]struct{}),
	}
	if err := s.loadSkipped(); err != nil {
		// Non-fatal: a corrupt skip file just means the user sees
		// the banner again. Log and continue.
		logging.L().Warn("update.skip.load_failed", "err", err.Error())
	}
	return s, nil
}

func (s *service) Check(ctx context.Context) (Info, error) {
	return s.checkChannel(ctx, "stable")
}

// checkChannel fetches the manifest for the given channel and
// resolves the Info. Used by both Check (default channel) and
// BackgroundPoll (configurable channel).
func (s *service) checkChannel(ctx context.Context, channel string) (Info, error) {
	url := s.cfg.ManifestURL
	if url == "" {
		url = channelManifestURL(channel)
	}
	m, err := fetchManifest(ctx, s.client, url)
	if errors.Is(err, errManifestNotFound) && channel == "prerelease" && s.cfg.ManifestURL == "" {
		// TODO(v0.4.x): once the prerelease manifest ships at
		// docs.kameas.ai we drop this fallback. Today we log and
		// fall back to stable so prerelease subscribers still see
		// updates.
		logging.L().Warn("update.manifest.prerelease_missing", "fallback", "stable")
		url = stableManifestURL
		m, err = fetchManifest(ctx, s.client, url)
	}
	info := Info{
		CurrentVersion: s.cfg.CurrentVersion,
		Channel:        channel,
	}
	if err != nil {
		return info, err
	}
	info.AvailableVersion = m.Version
	info.Notes = m.Notes

	if asset, ok := pickAssetFor(m, s.platform()); ok {
		info.DownloadURL = asset.URL
		info.Sha256 = asset.Sha256
	}

	info.Available = isNewer(s.cfg.CurrentVersion, m.Version)

	s.mu.RLock()
	_, skipped := s.skipped[m.Version]
	s.mu.RUnlock()
	info.SkippedByUser = skipped

	logging.L().Info("update.check",
		"channel", channel,
		"current", s.cfg.CurrentVersion,
		"available", info.AvailableVersion,
		"actionable", info.Available && !info.SkippedByUser,
	)
	return info, nil
}

// pickAssetFor picks an asset for an explicit platform tuple — used
// to honor Config.Platform overrides in tests without modifying
// runtime.GOOS.
func pickAssetFor(m manifest, platform string) (manifestAsset, bool) {
	for _, a := range m.Assets {
		if a.Platform == platform {
			return a, true
		}
	}
	return manifestAsset{}, false
}

func (s *service) platform() string {
	if s.cfg.Platform != "" {
		return s.cfg.Platform
	}
	return platformTuple()
}

// isNewer reports whether available is strictly newer than current
// per semver. Returns false when current is empty, "dev", or the two
// versions are equal/older.
//
// semver.Compare returns -1/0/+1 and tolerates a leading "v" — both
// "v0.4.0" and "0.4.0" parse correctly. An invalid version on either
// side returns 0 from Compare; we treat that as "no update" so a
// malformed manifest never accidentally claims "newer".
func isNewer(current, available string) bool {
	if current == "" || current == "dev" || available == "" {
		return false
	}
	if !semver.IsValid(ensureV(current)) || !semver.IsValid(ensureV(available)) {
		return false
	}
	return semver.Compare(ensureV(current), ensureV(available)) < 0
}

func ensureV(v string) string {
	if len(v) > 0 && v[0] == 'v' {
		return v
	}
	return "v" + v
}

// Download streams the asset to <DataDir>/update/staging/<basename>.
// Verifies sha256 inline (single-pass) so the file is hashed during
// the network read instead of after a second os.Open.
func (s *service) Download(ctx context.Context, info Info) (<-chan DownloadProgress, StagedUpdate, error) {
	if info.DownloadURL == "" {
		return nil, StagedUpdate{}, errors.New("update: info.DownloadURL empty (no asset for this platform?)")
	}
	if info.Sha256 == "" {
		return nil, StagedUpdate{}, errors.New("update: info.Sha256 empty (manifest must advertise digest)")
	}

	stagingDir := filepath.Join(s.cfg.DataDir, "update", "staging")
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return nil, StagedUpdate{}, fmt.Errorf("update: mkdir staging: %w", err)
	}
	dest := filepath.Join(stagingDir, defaultStagedName(s.platform(), info.AvailableVersion))

	progress := make(chan DownloadProgress, 32)
	staged := StagedUpdate{
		Path:          dest,
		TargetVersion: info.AvailableVersion,
		Sha256:        info.Sha256,
		Platform:      s.platform(),
	}

	go s.downloadPump(ctx, info, dest, progress)
	return progress, staged, nil
}

// downloadPump is the goroutine spawned by Download. It owns the
// progress channel for its lifetime; close(progress) signals "done".
func (s *service) downloadPump(ctx context.Context, info Info, dest string, progress chan<- DownloadProgress) {
	defer close(progress)

	// Open destination first; failure here is the fastest fail.
	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		progress <- DownloadProgress{Done: true, Err: fmt.Errorf("update: create staged %s: %w", tmp, err)}
		return
	}
	closed := false
	closeOnce := func() {
		if !closed {
			closed = true
			_ = f.Close()
		}
	}
	defer closeOnce()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, info.DownloadURL, nil)
	if err != nil {
		progress <- DownloadProgress{Done: true, Err: fmt.Errorf("update: build download request: %w", err)}
		return
	}
	resp, err := s.client.Do(req)
	if err != nil {
		progress <- DownloadProgress{Done: true, Err: fmt.Errorf("update: GET %s: %w", info.DownloadURL, err)}
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		progress <- DownloadProgress{Done: true, Err: fmt.Errorf("update: download %s returned %s", info.DownloadURL, resp.Status)}
		return
	}

	total := resp.ContentLength // -1 if unknown
	hasher := newSha256Writer()
	bytes := int64(0)
	buf := make([]byte, 64*1024)
	last := time.Time{}
	for {
		select {
		case <-ctx.Done():
			progress <- DownloadProgress{Bytes: bytes, Total: total, Done: true, Err: ctx.Err()}
			return
		default:
		}
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				progress <- DownloadProgress{Bytes: bytes, Total: total, Done: true, Err: fmt.Errorf("update: write staged: %w", werr)}
				return
			}
			_, _ = hasher.Write(buf[:n])
			bytes += int64(n)
			if time.Since(last) > 100*time.Millisecond {
				select {
				case progress <- DownloadProgress{Bytes: bytes, Total: total}:
				default:
				}
				last = time.Now()
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			progress <- DownloadProgress{Bytes: bytes, Total: total, Done: true, Err: fmt.Errorf("update: read body: %w", rerr)}
			return
		}
	}

	closeOnce()

	got := hasher.Sum()
	if !equalFold(got, info.Sha256) {
		_ = os.Remove(tmp)
		progress <- DownloadProgress{Bytes: bytes, Total: total, Done: true, Err: fmt.Errorf("%w: got %s want %s", errSha256Mismatch, got, info.Sha256)}
		return
	}

	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		progress <- DownloadProgress{Bytes: bytes, Total: total, Done: true, Err: fmt.Errorf("update: rename staged %s: %w", tmp, err)}
		return
	}

	logging.L().Info("update.download.done",
		"version", info.AvailableVersion,
		"bytes", bytes,
		"sha256", got,
		"platform", s.platform(),
	)
	progress <- DownloadProgress{Bytes: bytes, Total: total, Done: true}
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca := a[i]
		cb := b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 32
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 32
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// defaultStagedName picks a per-platform staged filename. macOS
// stages the .app bundle; Linux/Windows stage the executable.
func defaultStagedName(platform, version string) string {
	if version == "" {
		version = "next"
	}
	switch {
	case len(platform) >= 6 && platform[:6] == "darwin":
		return "Kenaz-" + version + ".app.new"
	case len(platform) >= 7 && platform[:7] == "windows":
		return "kenaz-harness-" + version + ".exe.new"
	default:
		return "kenaz-harness-" + version + ".new"
	}
}

// ApplyAndRestart performs the platform-specific swap via Swapper
// and exits. Tests inject a fake Swapper whose Restart records the
// call instead of os.Exit.
func (s *service) ApplyAndRestart(ctx context.Context, staged StagedUpdate) error {
	running := s.cfg.RunningBinaryPath
	if running == "" {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("update: resolve running binary: %w", err)
		}
		running = exe
	}
	logging.L().Info("update.apply.begin",
		"version", staged.TargetVersion,
		"platform", staged.Platform,
		"sha256", staged.Sha256,
	)
	if err := s.swapper.Swap(ctx, staged, running, s.cfg.DataDir); err != nil {
		logging.L().Warn("update.apply.failed", "err", err.Error())
		return err
	}
	logging.L().Info("update.apply.swapped", "version", staged.TargetVersion)
	return s.swapper.Restart(ctx, running)
}

// ListSkipped returns a snapshot of the persisted skip set. Sorted
// alphabetically so callers (and golden tests) get a deterministic
// order. The view layer (core/rpc/views/update) consumes this via an
// optional-interface assertion; tests injecting a fake Service may
// omit the method entirely.
func (s *service) ListSkipped() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.skipped))
	for v := range s.skipped {
		out = append(out, v)
	}
	// Cheap insertion sort; the skip set is bounded by the number of
	// releases the user has actively dismissed (~1-3 in practice).
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// UnskipVersion removes a version from the skip set so the banner
// re-appears on the next Check. The on-disk store is rewritten in the
// same atomic temp-rename pattern SkipVersion uses.
func (s *service) UnskipVersion(ctx context.Context, version string) error {
	_ = ctx
	if version == "" {
		return errors.New("update: UnskipVersion: empty version")
	}
	s.mu.Lock()
	if _, ok := s.skipped[version]; !ok {
		s.mu.Unlock()
		return nil
	}
	delete(s.skipped, version)
	versions := make([]string, 0, len(s.skipped))
	for v := range s.skipped {
		versions = append(versions, v)
	}
	s.mu.Unlock()
	if err := s.persistSkipped(versions); err != nil {
		return fmt.Errorf("update: persist unskip: %w", err)
	}
	logging.L().Info("update.unskip", "removed", true)
	return nil
}

// SkipVersion records `version` in the in-memory and on-disk skip
// set. Subsequent Check calls return Info{SkippedByUser: true} when
// the manifest version matches.
func (s *service) SkipVersion(ctx context.Context, version string) error {
	_ = ctx
	if version == "" {
		return errors.New("update: SkipVersion: empty version")
	}
	s.mu.Lock()
	s.skipped[version] = struct{}{}
	versions := make([]string, 0, len(s.skipped))
	for v := range s.skipped {
		versions = append(versions, v)
	}
	s.mu.Unlock()
	if err := s.persistSkipped(versions); err != nil {
		return fmt.Errorf("update: persist skip: %w", err)
	}
	logging.L().Info("update.skip", "version", version)
	return nil
}

// BackgroundPoll runs Check on the configured interval. The first
// poll runs immediately; later polls fire on the ticker. Errors are
// logged and retried — the caller never sees a transient network
// blip.
func (s *service) BackgroundPoll(ctx context.Context, interval time.Duration, channel string) error {
	if interval <= 0 {
		return errors.New("update: BackgroundPoll: interval must be > 0")
	}
	if channel == "" {
		channel = "stable"
	}
	t := time.NewTicker(interval)
	defer t.Stop()

	tick := func() {
		info, err := s.checkChannel(ctx, channel)
		if err != nil {
			logging.L().Warn("update.poll.failed", "channel", channel, "err", err.Error())
			return
		}
		if info.Available && !info.SkippedByUser && s.cfg.Publisher != nil {
			s.cfg.Publisher.Publish(AvailableTopic, info)
			logging.L().Info("update.poll.announced",
				"channel", channel,
				"version", info.AvailableVersion,
			)
		}
	}

	tick() // first poll runs immediately
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			tick()
		}
	}
}

// skipFilePath is the on-disk store for skipped versions.
func (s *service) skipFilePath() string {
	return filepath.Join(s.cfg.DataDir, "update", "skipped.json")
}

// loadSkipped reads the persisted skip set into memory. Missing file
// returns nil — fresh installs have nothing skipped.
func (s *service) loadSkipped() error {
	body, err := os.ReadFile(s.skipFilePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var versions []string
	if err := json.Unmarshal(body, &versions); err != nil {
		return err
	}
	s.mu.Lock()
	for _, v := range versions {
		s.skipped[v] = struct{}{}
	}
	s.mu.Unlock()
	return nil
}

func (s *service) persistSkipped(versions []string) error {
	if err := os.MkdirAll(filepath.Dir(s.skipFilePath()), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(versions, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.skipFilePath() + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.skipFilePath())
}
