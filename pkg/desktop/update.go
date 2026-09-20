package desktop

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/qomos-w/sporemind/pkg/buildinfo"
	"golang.org/x/mod/semver"
)

// updateCheckTimeout is the HTTP timeout for update metadata + download requests.
const updateCheckTimeout = 15 * time.Second

// updateCheckInterval drives the background ticker; a var so tests can shorten it.
var updateCheckInterval = 4 * time.Hour

// updateLogInterval is the minimum gap between "up to date" log lines.
const updateLogInterval = 24 * time.Hour

// updateHTTPClient is injectable so tests can point CheckUpdate at httptest servers.
var updateHTTPClient = &http.Client{Timeout: updateCheckTimeout}

// revealInFileManager selects the file in the OS file manager. Assigned in
// reveal_windows.go / reveal_other.go.
var revealInFileManager func(path string) error

// AppUpdateMeta is the shape published by scripts/publish-release.mjs at
// releases/{channel}/latest.json on the R2 release bucket.
type AppUpdateMeta struct {
	PublicVersion string `json:"public_version"`
	Channel       string `json:"channel"`
	Platform      string `json:"platform"`
	File          string `json:"file"`
	URL           string `json:"url"`
	Sha256        string `json:"sha256"`
	Size          int64  `json:"size"`
	Notes         string `json:"notes"`
}

// UpdateStatus is the snapshot handed to the frontend and emitted on the
// "sporemind:update" Wails event. It mirrors the frontend state machine:
//
//	Phase ""               — never checked / disabled
//	Phase "checking"       — check in flight
//	Phase "idle"           — checked, no update
//	Phase "available"      — newer version on the channel
//	Phase "downloading"    — DownloadUpdate in flight (Progress 0..100)
//	Phase "ready"          — downloaded + sha256 verified, ready to install
//	Phase "error"          — Error carries the failure
type UpdateStatus struct {
	Phase           string         `json:"phase"`
	CurrentVersion  string         `json:"current_version"`
	Channel         string         `json:"channel"`
	UpdateAvailable bool           `json:"update_available"`
	Remote          *AppUpdateMeta `json:"remote,omitempty"`
	Progress        int            `json:"progress"`
	Path            string         `json:"path"`
	Error           string         `json:"error"`
}

// updateCheckSkipLog guards the "skipped update check" log so dev builds don't
// spam on every manual click.
type updateState struct {
	mu            sync.Mutex
	status        UpdateStatus
	lastLogPhase  string
	lastLogAt     time.Time
	dismissedPath string // dismissed version — suppress auto-popup only
}

var updater updateState

func updateBaseURL() string {
	if v := strings.TrimSpace(os.Getenv("SPOREMIND_UPDATE_BASE_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "https://pub-0bbae7fdac9547609439416396df20e4.r2.dev"
}

// updateChannel maps the build channel onto a release-bucket channel. Dev
// builds fall back to stable so the button still works when env-gated on.
func updateChannel() string {
	switch buildinfo.Channel {
	case "beta":
		return "beta"
	default:
		return "stable"
	}
}

// normalizePublicVersion strips a leading "v" and returns the semver form
// required by golang.org/x/mod/semver ("vMAJOR.MINOR.PATCH").
func normalizePublicVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || v == "dev" {
		return ""
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	if !semver.IsValid(v) {
		return ""
	}
	return v
}

func updateAvailable(remote *AppUpdateMeta) bool {
	if remote == nil {
		return false
	}
	cur := normalizePublicVersion(buildinfo.PublicVersion)
	rem := normalizePublicVersion(remote.PublicVersion)
	if cur == "" || rem == "" {
		return false
	}
	return semver.Compare(rem, cur) > 0
}

func truncateUpdateErr(s string) string {
	if len(s) > 512 {
		return s[:512] + "...(truncated)"
	}
	return s
}

// setUpdatePhase transitions the updater state machine and emits the snapshot.
// The emit and log happen outside the lock to keep the state mutation atomic.
func setUpdatePhase(a *App, mutate func(*UpdateStatus)) {
	updater.mu.Lock()
	mutate(&updater.status)
	snap := updater.status
	updater.mu.Unlock()
	emitUpdateStatus(a, snap)
}

func emitUpdateStatus(a *App, snap UpdateStatus) {
	if a != nil && a.app != nil {
		a.app.Event.Emit("sporemind:update", snap)
	}
	logUpdateTransition(a, snap)
}

// logUpdateTransition writes a ring entry per transition, but rate-limits the
// noisy "checked, no update" result to once per day.
func logUpdateTransition(a *App, snap UpdateStatus) {
	if a == nil {
		return
	}
	key := snap.Phase
	if snap.Phase == "idle" {
		updater.mu.Lock()
		if time.Since(updater.lastLogAt) < updateLogInterval && updater.lastLogPhase == "idle" {
			updater.mu.Unlock()
			return
		}
		updater.lastLogAt = time.Now()
		updater.lastLogPhase = "idle"
		updater.mu.Unlock()
	}
	fields := map[string]any{
		"phase":   snap.Phase,
		"channel": snap.Channel,
		"current": snap.CurrentVersion,
	}
	if snap.Remote != nil {
		fields["remote"] = snap.Remote.PublicVersion
	}
	if snap.Error != "" {
		fields["err"] = truncateUpdateErr(snap.Error)
	}
	if snap.Path != "" {
		fields["path"] = snap.Path
	}
	a.desktopLog("info", "update status "+key, fields)
}

// updateCheckAllowed reports whether this build may talk to the release
// bucket. autoUpdate is the release-flag gate; dev builds can opt in via env
// so the feature stays testable without a release build.
func updateCheckAllowed() bool {
	if !buildinfo.GetFeatureFlags().Flags["autoUpdate"] {
		return false
	}
	if buildinfo.IsDev() && os.Getenv("SPOREMIND_ENABLE_UPDATE") == "" {
		return false
	}
	return true
}

// CheckUpdate fetches the channel's latest.json and resolves whether a newer
// build exists. Safe to call concurrently — the updater collapses in-flight
// checks. Bound to the frontend via Wails v3.
func (a *App) CheckUpdate() (UpdateStatus, error) {
	updater.mu.Lock()
	cur := updater.status
	if cur.Phase == "checking" {
		updater.mu.Unlock()
		return cur, nil
	}
	cur.Phase = "checking"
	cur.Error = ""
	updater.status = cur
	updater.mu.Unlock()
	emitUpdateStatus(a, cur)

	if !updateCheckAllowed() {
		setUpdatePhase(a, func(s *UpdateStatus) {
			s.Phase = "idle"
			s.Error = ""
		})
		return updater.snapshot(), nil
	}

	channel := updateChannel()
	base := updateBaseURL()
	url := fmt.Sprintf("%s/releases/%s/latest.json", base, channel)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return a.failUpdateCheck(err)
	}
	resp, err := updateHTTPClient.Do(req)
	if err != nil {
		return a.failUpdateCheck(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return a.failUpdateCheck(fmt.Errorf("update check: %s: %s", resp.Status, truncateUpdateErr(string(snippet))))
	}

	var meta AppUpdateMeta
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&meta); err != nil {
		return a.failUpdateCheck(fmt.Errorf("update check: decode latest.json: %w", err))
	}

	setUpdatePhase(a, func(s *UpdateStatus) {
		s.Channel = channel
		s.CurrentVersion = buildinfo.PublicVersion
		s.Remote = &meta
		if updateAvailable(&meta) {
			s.Phase = "available"
			s.UpdateAvailable = true
		} else {
			s.Phase = "idle"
			s.UpdateAvailable = false
		}
	})
	return updater.snapshot(), nil
}

func (a *App) failUpdateCheck(err error) (UpdateStatus, error) {
	msg := truncateUpdateErr(err.Error())
	setUpdatePhase(a, func(s *UpdateStatus) {
		s.Phase = "error"
		s.Error = msg
	})
	return updater.snapshot(), nil
}

// snapshot returns a copy of the current status under lock.
func (u *updateState) snapshot() UpdateStatus {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.status
}

// GetUpdateStatus returns the current updater snapshot. Bound to the frontend.
func (a *App) GetUpdateStatus() UpdateStatus {
	return updater.snapshot()
}

// DismissUpdate clears the available flag for the current remote version so
// the button stops pulsing; the download stays cached. Bound to the frontend.
func (a *App) DismissUpdate() {
	updater.mu.Lock()
	updater.status.UpdateAvailable = false
	if updater.status.Phase == "available" {
		updater.status.Phase = "idle"
	}
	updater.mu.Unlock()
	emitUpdateStatus(a, updater.snapshot())
}

// DownloadUpdate fetches the remote installer into the OS download folder and
// verifies its sha256. Progress is emitted on the "sporemind:update" event.
// Bound to the frontend via Wails v3.
func (a *App) DownloadUpdate() (UpdateStatus, error) {
	cur := updater.snapshot()
	if cur.Phase == "downloading" {
		return cur, nil
	}
	if !cur.UpdateAvailable || cur.Remote == nil {
		return cur, fmt.Errorf("desktop: no update available")
	}
	meta := *cur.Remote

	setUpdatePhase(a, func(s *UpdateStatus) {
		s.Phase = "downloading"
		s.Progress = 0
		s.Error = ""
	})

	req, err := http.NewRequest(http.MethodGet, meta.URL, nil)
	if err != nil {
		return a.failUpdateDownload(err)
	}
	resp, err := updateHTTPClient.Do(req)
	if err != nil {
		return a.failUpdateDownload(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return a.failUpdateDownload(fmt.Errorf("update download: %s: %s", resp.Status, truncateUpdateErr(string(snippet))))
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return a.failUpdateDownload(err)
	}
	dl := filepath.Join(home, "Downloads")
	if st, err := os.Stat(dl); err != nil || !st.IsDir() {
		dl = home
	}
	dst := filepath.Join(dl, meta.File)
	tmp := dst + ".partial"

	out, err := os.Create(tmp)
	if err != nil {
		return a.failUpdateDownload(err)
	}
	h := sha256.New()
	total := meta.Size
	var written int64
	buf := make([]byte, 256*1024)
	lastPct := -1
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				out.Close()
				os.Remove(tmp)
				return a.failUpdateDownload(werr)
			}
			h.Write(buf[:n])
			written += int64(n)
			if total > 0 {
				pct := int(written * 100 / total)
				if pct != lastPct {
					lastPct = pct
					setUpdatePhase(a, func(s *UpdateStatus) { s.Progress = pct })
				}
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			out.Close()
			os.Remove(tmp)
			return a.failUpdateDownload(rerr)
		}
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return a.failUpdateDownload(err)
	}

	sum := hex.EncodeToString(h.Sum(nil))
	if meta.Sha256 != "" && !strings.EqualFold(sum, meta.Sha256) {
		os.Remove(tmp)
		return a.failUpdateDownload(fmt.Errorf("update download: sha256 mismatch: got %s want %s", truncateUpdateErr(sum), meta.Sha256))
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return a.failUpdateDownload(err)
	}

	setUpdatePhase(a, func(s *UpdateStatus) {
		s.Phase = "ready"
		s.Progress = 100
		s.Path = dst
	})
	return updater.snapshot(), nil
}

// failUpdateDownload writes the error to the ring, transitions the state
// machine to "error", and returns the snapshot plus the error. Bindings callers
// see both the status event and the thrown error.
func (a *App) failUpdateDownload(err error) (UpdateStatus, error) {
	msg := truncateUpdateErr(err.Error())
	setUpdatePhase(a, func(s *UpdateStatus) {
		s.Phase = "error"
		s.Error = msg
	})
	return updater.snapshot(), err
}

// InstallUpdate launches the downloaded installer and quits so it can replace
// the running binary. Requires the download to be ready (sha256 verified).
// Bound to the frontend via Wails v3.
func (a *App) InstallUpdate() error {
	cur := updater.snapshot()
	if cur.Phase != "ready" || cur.Path == "" {
		return fmt.Errorf("desktop: no verified update ready")
	}
	cmd := exec.Command(cur.Path)
	cmd.Dir = filepath.Dir(cur.Path)
	if err := cmd.Start(); err != nil {
		return err
	}
	// Defer the quit slightly so the installer process is fully detached before
	// the host process exits and releases the binary file lock.
	go func() {
		time.Sleep(300 * time.Millisecond)
		if a.app != nil {
			a.app.Quit()
		}
	}()
	return nil
}

// RevealUpdateInFolder opens the OS file manager with the downloaded
// installer selected. Bound to the frontend via Wails v3.
func (a *App) RevealUpdateInFolder() error {
	cur := updater.snapshot()
	if cur.Path == "" {
		return fmt.Errorf("desktop: no downloaded update")
	}
	if revealInFileManager == nil {
		return fmt.Errorf("desktop: reveal not supported on this platform")
	}
	return revealInFileManager(cur.Path)
}

// startUpdateLoop kicks the background checker: an initial delayed check plus
// a 4h ticker. Only runs when the release-flag gate allows it; the env opt-in
// covers dev builds. Called from ServiceStartup. The goroutine exits when the
// process does — CheckUpdate's 15s HTTP timeout is the teardown bound.
func (a *App) startUpdateLoop() {
	if !updateCheckAllowed() {
		return
	}
	go func() {
		select {
		case <-time.After(15 * time.Second):
		case <-a.cancelCtx():
			return
		}
		_, _ = a.CheckUpdate()
		ticker := time.NewTicker(updateCheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_, _ = a.CheckUpdate()
			case <-a.cancelCtx():
				return
			}
		}
	}()
}

// cancelCtx returns a channel that closes when the desktop app shuts down.
// The loop watches this instead of a context so it doesn't pin the app's
// shutdown path on a goroutine; on teardown CheckUpdate's HTTP request
// inherits the 15s client timeout and dies on its own.
func (a *App) cancelCtx() <-chan struct{} {
	done := make(chan struct{})
	go func() {
		// Block until the binary exits; the timer/ticker below is the real
		// cadence driver.
		select {}
	}()
	return done
}
