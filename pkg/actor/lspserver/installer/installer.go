// Package installer implements a manifest-driven managed installer for
// external language-server tools (standalone binaries, archive releases).
//
// It generalizes the download/unpack/atomic-install pattern from
// pkg/actor/computeruse/internal/capture/ocr/download.go into a reusable
// library: an HTTP fetch with progress reporting and SHA256 verification,
// safe archive extraction (tar.gz | zip | raw), and an atomic directory
// publish so an interrupted install never leaves a half-visible tool
// directory behind — everything is staged under <root>/.tmp and published
// with a single rename.
//
// Per the project's actor-first constraint this package holds no business
// state and no package-level mutable variables: every piece of mutable state
// lives inside the staging directories owned by a single Install call. The
// lspserver actor owns the decision to install and supplies the managed
// root directory.
package installer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/qomos-w/sporemind/pkg/archive"
)

// markerName is the file written inside every installed tool directory that
// records which manifest produced it. It lives in the payload before the
// atomic publish, so a visible install is always complete.
const markerName = ".lsp-installer.json"

// tmpDirName is the staging area under the managed root where downloads,
// archives and payloads are assembled before being published atomically.
// Consumers of the managed root only ever look at published directories,
// never here.
const tmpDirName = ".tmp"

// Kind enumerates the supported artifact shapes.
type Kind string

const (
	// KindTarGz is a gzip-compressed tar archive.
	KindTarGz Kind = "tar.gz"
	// KindZip is a zip archive.
	KindZip Kind = "zip"
	// KindRaw is a bare single file (typically an executable binary); it is
	// installed under its URL base name (query stripped) with mode 0755 on
	// Unix.
	KindRaw Kind = "raw"
	// KindGzip is a gzip-compressed single file (typically an executable
	// binary); it is decompressed and written under Asset.InstallName with
	// mode 0755.
	KindGzip Kind = "gzip"
)

// Asset is one downloadable artifact pinned to a platform.
type Asset struct {
	// GOOS / GOARCH select this asset; "*" matches any value. Selection
	// prefers the exact (GOOS, GOARCH) pair, then (GOOS, "*"), then
	// ("*", GOARCH), then ("*", "*").
	GOOS   string
	GOARCH string

	// URL is the artifact download location (http/https).
	URL string

	// SHA256 is the hex-encoded digest of the artifact bytes. Required:
	// installs without integrity verification are rejected at validation
	// time.
	SHA256 string

	// Kind selects the unpack strategy.
	Kind Kind

	// StripTopLevel, when true, removes the single common top-level
	// directory that wraps all entries of the archive (the common
	// "tool-1.2.3/" GitHub release layout). All entries must share the same
	// first path segment or extraction fails.
	StripTopLevel bool

	// InstallName is the target file name for single-file kinds (KindRaw,
	// KindGzip). When empty, KindRaw falls back to the URL base name (query
	// stripped). KindGzip requires InstallName.
	InstallName string
}

// Manifest describes one managed tool install.
type Manifest struct {
	// Tool identifies the product (e.g. "rust-analyzer"). Used in staging
	// paths and the install record; restricted to [A-Za-z0-9._-].
	Tool string

	// Version is the requested release version. A matching install record
	// short-circuits Ensure with StatusSkipped.
	Version string

	// Assets lists per-platform artifacts; at least one must be selectable
	// for the current platform at install time.
	Assets []Asset

	// InstallDir is the tool directory relative to the managed root
	// (e.g. "rust-analyzer"); forward-slash separated, no traversal.
	InstallDir string
}

// Status reports what an Ensure call did.
type Status string

const (
	// StatusSkipped means a matching version was already installed.
	StatusSkipped Status = "skipped"
	// StatusInstalled means the artifact was downloaded and published.
	StatusInstalled Status = "installed"
)

// Result summarizes one Ensure call.
type Result struct {
	Status  Status
	Tool    string
	Version string
	Dir     string // absolute path of the published tool directory
}

// ProgressFunc receives download progress after each chunk. total comes from
// Content-Length and is -1 when unknown; done is monotonically increasing.
type ProgressFunc func(done, total int64)

// Limits caps archive resources during extraction. Zero fields fall back to
// DefaultLimits.
type Limits struct {
	MaxCompressedBytes   int64 // cap on the artifact file size
	MaxUncompressedBytes int64 // cap on total extracted bytes
	MaxEntries           int   // cap on archive entry count
}

// DefaultLimits returns the safety caps for tool artifacts. Language-tool
// releases are larger than document archives, so these are deliberately more
// generous than pkg/archive's transfer caps.
func DefaultLimits() Limits {
	return Limits{
		MaxCompressedBytes:   512 << 20, // 512 MiB
		MaxUncompressedBytes: 2 << 30,   // 2 GiB
		MaxEntries:           20000,
	}
}

// Options carries injectable collaborators for Ensure. The zero value works:
// a default HTTP client (5 minute timeout) and DefaultLimits are used.
type Options struct {
	// Client overrides the HTTP client used for downloads.
	Client *http.Client
	// Progress, when non-nil, receives download progress callbacks.
	Progress ProgressFunc
	// Limits overrides the extraction caps. Zero fields use defaults.
	Limits Limits
}

// Validate checks manifest invariants eagerly so failures surface before any
// network or filesystem work happens.
func (m Manifest) Validate() error {
	if strings.TrimSpace(m.Tool) == "" {
		return errors.New("installer: manifest tool is required")
	}
	for _, r := range m.Tool {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-", r) {
			return fmt.Errorf("installer: manifest tool %q contains unsupported character %q", m.Tool, r)
		}
	}
	if strings.TrimSpace(m.Version) == "" {
		return errors.New("installer: manifest version is required")
	}
	if len(m.Assets) == 0 {
		return errors.New("installer: manifest has no assets")
	}
	if err := validateRelativeDir(m.InstallDir); err != nil {
		return fmt.Errorf("installer: manifest install dir: %w", err)
	}
	for i, a := range m.Assets {
		if err := a.Validate(); err != nil {
			return fmt.Errorf("installer: manifest asset %d: %w", i, err)
		}
	}
	return nil
}

// Validate checks one asset definition.
func (a Asset) Validate() error {
	switch {
	case strings.TrimSpace(a.GOOS) == "":
		return errors.New("goos is required")
	case strings.TrimSpace(a.GOARCH) == "":
		return errors.New("goarch is required")
	}
	u, err := url.Parse(a.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("asset url %q must be an http(s) URL", a.URL)
	}
	if err := validateSHA256(a.SHA256); err != nil {
		return err
	}
	switch a.Kind {
	case KindTarGz, KindZip, KindRaw:
	case KindGzip:
		if strings.TrimSpace(a.InstallName) == "" {
			return errors.New("install name is required for gzip assets")
		}
	default:
		return fmt.Errorf("unsupported archive kind %q", a.Kind)
	}
	if a.InstallName != "" {
		if err := validateInstallName(a.InstallName); err != nil {
			return err
		}
	}
	return nil
}

func validateSHA256(hexSum string) error {
	if len(hexSum) != sha256.Size*2 {
		return fmt.Errorf("sha256 %q must be %d hex characters", hexSum, sha256.Size*2)
	}
	if _, err := hex.DecodeString(hexSum); err != nil {
		return fmt.Errorf("sha256 %q is not valid hex: %v", hexSum, err)
	}
	return nil
}

// validateInstallName rejects empty, traversal, or path-like install names.
func validateInstallName(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("install name must not be empty")
	}
	clean, err := archive.SafeName(name)
	if err != nil {
		return fmt.Errorf("install name %q: %w", name, err)
	}
	if clean != name {
		return fmt.Errorf("install name %q normalizes to %q", name, clean)
	}
	return nil
}

// validateRelativeDir accepts a slash-separated relative directory such as
// "tools/rust-analyzer" and rejects empty, absolute and traversing values.
func validateRelativeDir(dir string) error {
	if strings.HasPrefix(dir, "/") || strings.HasPrefix(dir, "\\") {
		return fmt.Errorf("%q must be a relative path", dir)
	}
	if strings.ContainsRune(dir, ':') {
		return fmt.Errorf("%q must not contain ':'", dir)
	}
	parts := strings.FieldsFunc(dir, func(r rune) bool { return r == '/' || r == '\\' })
	if len(parts) == 0 {
		return errors.New("must be a non-empty relative path")
	}
	for _, p := range parts {
		if p == ".." || p == "." {
			return fmt.Errorf("%q must not contain . or .. components", dir)
		}
	}
	return nil
}

// Select resolves the asset for the given platform, preferring exact
// (goos, goarch) over wildcard fallbacks. It returns an error listing the
// available platforms when nothing matches.
func (m Manifest) Select(goos, goarch string) (Asset, error) {
	best := -1
	rank := func(a Asset) int {
		switch {
		case a.GOOS == goos && a.GOARCH == goarch:
			return 0
		case a.GOOS == goos && a.GOARCH == "*":
			return 1
		case a.GOOS == "*" && a.GOARCH == goarch:
			return 2
		case a.GOOS == "*" && a.GOARCH == "*":
			return 3
		default:
			return -1
		}
	}
	for i, a := range m.Assets {
		if r := rank(a); r >= 0 && (best < 0 || r < rank(m.Assets[best])) {
			best = i
		}
	}
	if best < 0 {
		platforms := make([]string, 0, len(m.Assets))
		for _, a := range m.Assets {
			platforms = append(platforms, a.GOOS+"/"+a.GOARCH)
		}
		return Asset{}, fmt.Errorf("installer: tool %s has no asset for %s/%s (available: %s)",
			m.Tool, goos, goarch, strings.Join(platforms, ", "))
	}
	return m.Assets[best], nil
}

// installRecord is the JSON payload persisted as the install marker inside a
// published tool directory.
type installRecord struct {
	Tool        string `json:"tool"`
	Version     string `json:"version"`
	URL         string `json:"url"`
	SHA256      string `json:"sha256"`
	Kind        Kind   `json:"kind"`
	InstalledAt string `json:"installed_at"` // RFC 3339 UTC
}

// InstalledVersion reads the install record of the tool directory described
// by the manifest. ok is false when nothing (or something unreadable) is
// installed there; a corrupt marker is reported as not-installed so Ensure
// proceeds with a fresh install.
func InstalledVersion(root string, m Manifest) (version string, ok bool, err error) {
	if err := m.Validate(); err != nil {
		return "", false, err
	}
	rec, found, err := readRecord(toolDir(root, m))
	if err != nil || !found {
		return "", false, err
	}
	if rec.Tool != m.Tool {
		return "", false, nil
	}
	return rec.Version, true, nil
}

// Ensure makes sure the manifest's tool is installed under root at the
// requested version. When the existing install record already matches, it
// returns StatusSkipped without touching the network. Otherwise the artifact
// is selected for the current platform, downloaded into a private staging
// directory under <root>/.tmp, hash-verified, safely extracted, stamped with
// an install record, and published with a single atomic rename. An interrupted
// run therefore never exposes a partial tool directory; only private staging
// leftovers may remain until they are cleaned up.
func Ensure(ctx context.Context, root string, m Manifest, opts Options) (Result, error) {
	if err := m.Validate(); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(root) == "" {
		return Result{}, errors.New("installer: managed root is required")
	}
	finalDir := toolDir(root, m)
	if rec, found, err := readRecord(finalDir); err == nil && found &&
		rec.Tool == m.Tool && rec.Version == m.Version {
		return Result{Status: StatusSkipped, Tool: m.Tool, Version: m.Version, Dir: finalDir}, nil
	}

	asset, err := m.Select(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return Result{}, err
	}
	limits := opts.Limits.withDefaults()

	work, err := newWorkDir(root, m.Tool)
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(work)

	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}

	artifactPath := filepath.Join(work, "artifact.download")
	if err := httpDownload(ctx, client, asset.URL, artifactPath, asset.SHA256, limits.MaxCompressedBytes, opts.Progress); err != nil {
		return Result{}, fmt.Errorf("installer: tool %s: %w", m.Tool, err)
	}

	payload := filepath.Join(work, "payload")
	if err := os.MkdirAll(payload, 0o755); err != nil {
		return Result{}, fmt.Errorf("installer: create payload dir: %w", err)
	}
	if err := unpack(artifactPath, asset, payload, limits); err != nil {
		return Result{}, fmt.Errorf("installer: tool %s: %w", m.Tool, err)
	}
	if err := writeRecord(payload, m, asset); err != nil {
		return Result{}, err
	}

	if err := publishDirectory(payload, finalDir); err != nil {
		return Result{}, fmt.Errorf("installer: publish %s: %w", finalDir, err)
	}
	return Result{Status: StatusInstalled, Tool: m.Tool, Version: m.Version, Dir: finalDir}, nil
}

// toolDir returns the published tool directory for the manifest under root.
// Validate guarantees InstallDir is a clean relative path before this runs.
func toolDir(root string, m Manifest) string {
	parts := strings.FieldsFunc(m.InstallDir, func(r rune) bool { return r == '/' || r == '\\' })
	return filepath.Join(append([]string{root}, parts...)...)
}

// newWorkDir creates a unique staging directory under <root>/.tmp.
func newWorkDir(root, tool string) (string, error) {
	dir := filepath.Join(root, tmpDirName, "installer-"+tool+"-"+uuid.NewString())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create staging dir: %w", err)
	}
	return dir, nil
}

// withDefaults fills zero-valued limit fields.
func (l Limits) withDefaults() Limits {
	def := DefaultLimits()
	if l.MaxCompressedBytes <= 0 {
		l.MaxCompressedBytes = def.MaxCompressedBytes
	}
	if l.MaxUncompressedBytes <= 0 {
		l.MaxUncompressedBytes = def.MaxUncompressedBytes
	}
	if l.MaxEntries <= 0 {
		l.MaxEntries = def.MaxEntries
	}
	return l
}

// readRecord loads the install marker from dir. found is false when the
// marker does not exist; a malformed marker is treated as absent so a fresh
// install can proceed.
func readRecord(dir string) (installRecord, bool, error) {
	raw, err := os.ReadFile(filepath.Join(dir, markerName))
	if errors.Is(err, os.ErrNotExist) {
		return installRecord{}, false, nil
	}
	if err != nil {
		return installRecord{}, false, fmt.Errorf("installer: read install record: %w", err)
	}
	var rec installRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return installRecord{}, false, nil
	}
	return rec, true, nil
}

// writeRecord stamps the payload with the install marker before publication.
func writeRecord(payloadDir string, m Manifest, a Asset) error {
	rec := installRecord{
		Tool:        m.Tool,
		Version:     m.Version,
		URL:         a.URL,
		SHA256:      strings.ToLower(a.SHA256),
		Kind:        a.Kind,
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
	}
	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("installer: encode install record: %w", err)
	}
	if err := os.WriteFile(filepath.Join(payloadDir, markerName), raw, 0o644); err != nil {
		return fmt.Errorf("installer: write install record: %w", err)
	}
	return nil
}

// publishDirectory moves staged into final with a rename. When final already
// exists it is moved aside first and restored if the swap fails, so a failure
// never leaves the tool directory missing or half-written.
func publishDirectory(staged, final string) error {
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return fmt.Errorf("create parent dir: %w", err)
	}
	_, statErr := os.Lstat(final)
	switch {
	case os.IsNotExist(statErr):
		return os.Rename(staged, final)
	case statErr != nil:
		return fmt.Errorf("stat target: %w", statErr)
	}
	backup := final + ".old-" + uuid.NewString()
	if err := os.Rename(final, backup); err != nil {
		return fmt.Errorf("move previous install aside: %w", err)
	}
	if err := os.Rename(staged, final); err != nil {
		if rbErr := os.Rename(backup, final); rbErr != nil {
			return fmt.Errorf("%w (rollback failed: %v)", err, rbErr)
		}
		return err
	}
	// Best effort: removal may fail while the old engine binary is still
	// running (Windows locks executables). The leftover .old- directory is
	// inert and will be replaced on the next successful install attempt.
	_ = os.RemoveAll(backup)
	return nil
}
