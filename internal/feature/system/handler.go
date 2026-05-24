package system

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"sync"

	commonExec "github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/common/lifecycle"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
	sfdb "github.com/svrforum/SFPanel/internal/db"
	"github.com/svrforum/SFPanel/internal/release"
)

// updateMu serialises in-process update operations. Two simultaneous
// `POST /api/v1/system/update` calls would otherwise race on `execPath+".new"`
// and end up renaming a half-written temp file over /usr/local/bin/sfpanel.
var updateMu sync.Mutex

// restoreMu serialises in-process restore operations. Concurrent restores
// would race on the .new/.bak filenames and produce inconsistent state.
// Distinct from updateMu so the two operations have independent 409 paths:
// a restore that fires during an update is refused on its own merits.
var restoreMu sync.Mutex

// releaseAPIURL is the GitHub Releases endpoint we poll for new versions.
// Exposed as a package-level var (rather than a const) so tests can point
// CheckUpdate / RunUpdate at an httptest server without making outbound
// network calls.
var releaseAPIURL = "https://api.github.com/repos/svrforum/SFPanel/releases/latest"

// exitProcess is the function the no-systemd restart fallback calls after
// flushing its response. It's a var so unit tests can swap in a no-op and
// avoid actually killing the test process when they drive RestoreBackup /
// RunUpdate through to the supervisor-less branch.
var exitProcess = func() { os.Exit(0) }

// isSystemdActive is the systemd-presence probe used by RunUpdate and
// RestoreBackup. It's a var (default = lifecycle.IsSystemdActive) so unit
// tests can force either branch without relying on whether the host the
// test is running on happens to have /run/systemd/system — dev workstations
// running the panel as `go run` against a real systemd host would otherwise
// produce different test outcomes than CI.
var isSystemdActive = lifecycle.IsSystemdActive

// osExecutable resolves the running binary's path during the update swap.
// It's a var (default = os.Executable) so tests can point the .bak/.new
// staging at a controlled directory — e.g. to force the .bak backup write
// to fail and assert the update aborts rather than swapping the binary
// without a rollback copy on disk.
var osExecutable = os.Executable

// maxUpdateArchiveBytes caps the downloaded archive at 200 MiB. Keeping the
// limit on the wire (LimitReader) and on disk (size check) prevents a
// compromised release host from filling the disk during the verify step.
const maxUpdateArchiveBytes int64 = 200 * 1024 * 1024

// maxBinaryEntryBytes caps the decompressed size of the sfpanel binary
// inside the update tarball. A malicious archive can compress 1000:1
// (mostly zeros), so the on-wire 200 MiB cap doesn't bound decompression.
// 256 MiB is ~5× the real binary size (~50 MB) and well below typical free
// RAM. RestoreBackup applies the same kind of cap per entry (see
// maxEntrySize below, shipped in v0.15.0 hardening Task 2.5).
const maxBinaryEntryBytes int64 = 256 * 1024 * 1024

type Handler struct {
	Version     string
	// DB is the live SQLite connection — used to force a WAL checkpoint
	// before copying the DB file to .bak so the snapshot is not stale.
	// Nil-safe: pre-update backup falls back to a plain file copy.
	DB          *sql.DB
	DBPath      string
	ConfigPath  string
	ComposePath string
	// Port is the HTTP listen port — used by the update watchdog to compose
	// the local health-check URL after binary swap. Zero means "skip
	// watchdog rollback" (gracefully degrades to the pre-watchdog behavior).
	Port int
	Cmd  commonExec.Commander
	// ClusterMgr is the cluster manager when this node is part of a Raft
	// cluster; nil in standalone mode. RunUpdate consults it to enforce a
	// quorum guard before taking the node offline, so an operator running
	// `for n in nodes; ssh $n sudo sfpanel update`-style fan-out can't
	// inadvertently take every voter down at once.
	ClusterMgr *cluster.Manager
}

type GitHubRelease struct {
	TagName     string          `json:"tag_name"`
	Body        string          `json:"body"`
	PublishedAt string          `json:"published_at"`
	Assets      []release.Asset `json:"assets"`
}

type UpdateCheckResponse struct {
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version"`
	UpdateAvailable bool   `json:"update_available"`
	ReleaseNotes    string `json:"release_notes"`
	PublishedAt     string `json:"published_at"`
}

// CheckUpdate queries GitHub releases API and returns version comparison.
func (h *Handler) CheckUpdate(c echo.Context) error {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(releaseAPIURL)
	if err != nil {
		return response.Fail(c, http.StatusBadGateway, response.ErrUpdateCheckFailed, "Failed to check for updates")
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return response.Fail(c, http.StatusBadGateway, response.ErrUpdateCheckFailed,
			fmt.Sprintf("GitHub API returned %d", resp.StatusCode))
	}

	var ghRelease GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&ghRelease); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrUpdateCheckFailed, "Failed to parse release info")
	}

	latest := strings.TrimPrefix(ghRelease.TagName, "v")
	current := strings.TrimPrefix(h.Version, "v")
	return response.OK(c, UpdateCheckResponse{
		CurrentVersion:  current,
		LatestVersion:   latest,
		UpdateAvailable: latest != current,
		ReleaseNotes:    ghRelease.Body,
		PublishedAt:     ghRelease.PublishedAt,
	})
}

// RunUpdate downloads the latest release and replaces the current binary, streaming progress via SSE.
func (h *Handler) RunUpdate(c echo.Context) error {
	// Refuse a second concurrent update — two callers fighting over execPath+".new"
	// can install a truncated binary if both writes interleave before the rename.
	if !updateMu.TryLock() {
		return response.Fail(c, http.StatusConflict, response.ErrUpdateInProgress, "Another update is already running")
	}
	defer updateMu.Unlock()

	// Cluster quorum guard. Without this, fanning out
	// `for n in nodes; ssh $n sudo curl ... /system/update` takes every
	// voter offline at the same time and Raft loses quorum until the
	// slowest node's download + restart finishes — minutes-long blackout
	// on a slow link. ClusterUpdate orchestrates a rolling restart with
	// its own quorum check; this guard is the second line of defense for
	// when an operator bypasses the orchestrator.
	if err := h.clusterUpdateQuorumGuard(c); err != nil {
		return err
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(releaseAPIURL)
	if err != nil {
		return response.Fail(c, http.StatusBadGateway, response.ErrUpdateFailed, "Failed to check for updates")
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return response.Fail(c, http.StatusBadGateway, response.ErrUpdateFailed, "GitHub API error")
	}

	var ghRelease GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&ghRelease); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrUpdateFailed, "Failed to parse release")
	}
	latest := strings.TrimPrefix(ghRelease.TagName, "v")
	current := strings.TrimPrefix(h.Version, "v")
	if latest == current {
		return response.OK(c, map[string]string{"status": "up_to_date"})
	}
	// Block downgrades: a poisoned upstream response that returns a known-vulnerable
	// older tag would otherwise silently roll the binary backwards.
	if forward, vErr := release.IsForwardUpdate(current, latest); vErr != nil {
		return response.Fail(c, http.StatusBadGateway, response.ErrUpdateFailed,
			fmt.Sprintf("Cannot compare versions: %v", vErr))
	} else if !forward {
		return response.Fail(c, http.StatusConflict, response.ErrUpdateDowngrade,
			fmt.Sprintf("Refusing to downgrade %s → %s", current, latest))
	}

	// SSE setup
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")
	c.Response().WriteHeader(http.StatusOK)
	flusher := c.Response()

	sendEvent := func(step, message string) {
		data, _ := json.Marshal(map[string]string{"step": step, "message": message})
		fmt.Fprintf(flusher, "data: %s\n\n", data)
		flusher.Flush()
	}

	// Download
	arch := runtime.GOARCH
	archiveName := fmt.Sprintf("sfpanel_%s_linux_%s.tar.gz", latest, arch)
	url := release.FindAssetURL(ghRelease.Assets, archiveName)
	if url == "" {
		sendEvent("error", fmt.Sprintf("Release asset not found: %s", archiveName))
		return nil
	}
	checksumsURL := release.FindAssetURL(ghRelease.Assets, "checksums.txt")
	if checksumsURL == "" {
		sendEvent("error", "Release checksums.txt not found; refusing unsigned update")
		return nil
	}
	sendEvent("downloading", fmt.Sprintf("Downloading v%s (%s)...", latest, arch))

	dlClient := &http.Client{Timeout: 5 * time.Minute}
	dlResp, err := dlClient.Get(url)
	if err != nil {
		sendEvent("error", fmt.Sprintf("Download failed: %v", err))
		return nil
	}
	defer dlResp.Body.Close()
	if dlResp.StatusCode != 200 {
		sendEvent("error", fmt.Sprintf("Download failed (HTTP %d)", dlResp.StatusCode))
		return nil
	}

	sendEvent("verifying", "Downloading checksums...")
	checksumResp, err := dlClient.Get(checksumsURL)
	if err != nil {
		sendEvent("error", fmt.Sprintf("Checksum download failed: %v", err))
		return nil
	}
	defer checksumResp.Body.Close()
	if checksumResp.StatusCode != 200 {
		sendEvent("error", fmt.Sprintf("Checksum download failed (HTTP %d)", checksumResp.StatusCode))
		return nil
	}

	checksumBody, err := io.ReadAll(checksumResp.Body)
	if err != nil {
		sendEvent("error", fmt.Sprintf("Checksum read failed: %v", err))
		return nil
	}

	// Cosign keyless signature verification of checksums.txt before we trust
	// any hash inside it. The cert pins the upload to release.yml on the
	// canonical repo for a tagged version — so if the GitHub releases page
	// is compromised but the workflow isn't, this catches it.
	//
	// Releases at or after release.SignatureRequiredSince MUST carry both
	// signature assets — missing them aborts the update to prevent a
	// supply-chain downgrade where an attacker who controls the GitHub
	// Release simply deletes the .sig/.pem to fall through to SHA-only
	// (which is no defence at all when they can also rewrite checksums.txt).
	// Pre-cutoff targets fall back to SHA-256 only so the one-time upgrade
	// path from old unsigned releases still works.
	sigURL := release.FindAssetURL(ghRelease.Assets, "checksums.txt.sig")
	certURL := release.FindAssetURL(ghRelease.Assets, "checksums.txt.pem")
	if sigURL == "" || certURL == "" {
		if release.SignatureRequiredFor(latest) {
			sendEvent("error", fmt.Sprintf(
				"Signature required: release %s is missing checksums.txt.sig or checksums.txt.pem (refusing update to prevent supply-chain downgrade)", latest))
			return nil
		}
		sendEvent("verifying", "Release predates Sigstore signing; falling back to SHA-256 only")
	} else {
		sendEvent("verifying", "Verifying release signature (Sigstore keyless)...")
		sigBytes, sigErr := fetchBytes(dlClient, sigURL)
		if sigErr != nil {
			sendEvent("error", fmt.Sprintf("Signature download failed: %v", sigErr))
			return nil
		}
		certBytes, certErr := fetchBytes(dlClient, certURL)
		if certErr != nil {
			sendEvent("error", fmt.Sprintf("Cert download failed: %v", certErr))
			return nil
		}
		if vErr := release.VerifyCosignBlob(checksumBody, sigBytes, certBytes, release.SFPanelReleaseIdentity()); vErr != nil {
			sendEvent("error", fmt.Sprintf("Signature verification failed: %v", vErr))
			return nil
		}
	}

	expectedSHA256, err := release.ParseExpectedSHA256(checksumBody, archiveName)
	if err != nil {
		sendEvent("error", err.Error())
		return nil
	}

	// Stream the archive to a temp file rather than buffering 200 MiB in RAM.
	// Small (256–512 MB) cluster nodes were OOM-killed mid-update with the old
	// io.ReadAll path. Hash is computed in the same pass via TeeReader.
	tmpDir, err := os.MkdirTemp("", "sfpanel-update-*")
	if err != nil {
		sendEvent("error", fmt.Sprintf("Cannot create temp dir: %v", err))
		return nil
	}
	defer os.RemoveAll(tmpDir)
	archivePath := filepath.Join(tmpDir, archiveName)
	archiveFile, err := os.Create(archivePath)
	if err != nil {
		sendEvent("error", fmt.Sprintf("Cannot open archive temp: %v", err))
		return nil
	}
	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(archiveFile, hasher),
		io.LimitReader(dlResp.Body, maxUpdateArchiveBytes+1))
	closeErr := archiveFile.Close()
	if err != nil {
		sendEvent("error", fmt.Sprintf("Download read failed: %v", err))
		return nil
	}
	if closeErr != nil {
		sendEvent("error", fmt.Sprintf("Archive flush failed: %v", closeErr))
		return nil
	}
	if written > maxUpdateArchiveBytes {
		sendEvent("error", fmt.Sprintf("Archive exceeds %d bytes, refusing to install", maxUpdateArchiveBytes))
		return nil
	}
	actualSHA256 := hex.EncodeToString(hasher.Sum(nil))
	if actualSHA256 != expectedSHA256 {
		sendEvent("error", "Checksum verification failed")
		return nil
	}

	// Extract
	sendEvent("extracting", "Extracting binary...")
	archiveReader, err := os.Open(archivePath)
	if err != nil {
		sendEvent("error", fmt.Sprintf("Re-open archive failed: %v", err))
		return nil
	}
	defer archiveReader.Close()
	gzr, err := gzip.NewReader(archiveReader)
	if err != nil {
		sendEvent("error", fmt.Sprintf("Decompression failed: %v", err))
		return nil
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	var binaryData []byte
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			sendEvent("error", fmt.Sprintf("Archive read failed: %v", err))
			return nil
		}
		if hdr.Name == "sfpanel" || strings.HasSuffix(hdr.Name, "/sfpanel") {
			// LimitReader at cap+1 so a result length > cap proves the
			// entry overflowed (LimitReader at exactly the cap can't
			// distinguish "fits exactly" from "truncated"). A malicious
			// tarball can pack 1000:1-compressing zero payloads to
			// trigger an OOM here — refuse anything larger than the cap.
			limited := io.LimitReader(tr, maxBinaryEntryBytes+1)
			binaryData, err = io.ReadAll(limited)
			if err != nil {
				sendEvent("error", fmt.Sprintf("Binary read failed: %v", err))
				return nil
			}
			if int64(len(binaryData)) > maxBinaryEntryBytes {
				sendEvent("error", fmt.Sprintf("binary exceeds size cap (%d bytes)", maxBinaryEntryBytes))
				return nil
			}
			break
		}
	}
	if binaryData == nil {
		sendEvent("error", "Binary not found in archive")
		return nil
	}

	// Replace binary
	sendEvent("replacing", "Replacing binary...")
	execPath, err := osExecutable()
	if err != nil {
		sendEvent("error", fmt.Sprintf("Cannot find binary path: %v", err))
		return nil
	}

	backupPath := execPath + ".bak"
	if data, readErr := os.ReadFile(execPath); readErr == nil {
		// A failed .bak write must abort the update: without it, an update
		// that later fails has no rollback copy and the panel is bricked.
		if err := os.WriteFile(backupPath, data, 0755); err != nil {
			sendEvent("error", fmt.Sprintf("Backup failed: %v", err))
			return nil
		}
	} else {
		sendEvent("error", fmt.Sprintf("Cannot read current binary for backup: %v", readErr))
		return nil
	}

	// Force WAL pages back into the main DB file so the .bak we're about
	// to write is a complete snapshot. Without this the live process's
	// pending writes would still live in sfpanel.db-wal, and rolling
	// back to .bak would silently drop them.
	if h.DB != nil {
		if cpErr := sfdb.CheckpointWAL(h.DB); cpErr != nil {
			slog.Warn("WAL checkpoint failed before DB backup; .bak may be stale", "error", cpErr)
		}
	}
	if data, readErr := os.ReadFile(h.DBPath); readErr == nil {
		_ = os.WriteFile(h.DBPath+".bak", data, 0600)
	}
	if data, readErr := os.ReadFile(h.ConfigPath); readErr == nil {
		_ = os.WriteFile(h.ConfigPath+".bak", data, 0600)
	}

	tmpPath := execPath + ".new"
	if err := os.WriteFile(tmpPath, binaryData, 0755); err != nil {
		sendEvent("error", fmt.Sprintf("Write failed: %v", err))
		return nil
	}
	if err := os.Rename(tmpPath, execPath); err != nil {
		os.Remove(tmpPath)
		sendEvent("error", fmt.Sprintf("Replace failed: %v", err))
		return nil
	}

	// Restart
	sendEvent("restarting", "Restarting service...")
	// Bring any pre-existing systemd unit up to date before the restart
	// so operators who installed under the old Restart=on-failure policy
	// inherit Restart=always without having to re-run install.sh.
	if migrated, migrateErr := lifecycle.MigrateRestartPolicy(); migrateErr != nil {
		slog.Warn("systemd unit migration failed during update", "error", migrateErr)
	} else if migrated {
		sendEvent("restarting", "Migrated systemd unit (Restart=on-failure → Restart=always)")
	}
	// Spawn an external watchdog process from the BACKUP binary. If the new
	// binary fails to come up within 90 s the watchdog restores .bak and
	// restarts sfpanel — without this, a botched update leaves systemd
	// in a Restart=always loop on a broken binary forever.
	//
	// Forward-only: spawning is best-effort. Pre-watchdog binaries used as
	// .bak don't know the subcommand and will exit 2 — the rollback path
	// still works as long as either bound is the new code, which is the
	// common case after this lands.
	if h.Port > 0 {
		watchdogCmd := exec.Command(backupPath, "watchdog-update",
			backupPath,
			execPath,
			fmt.Sprintf("http://127.0.0.1:%d/api/v1/system/info", h.Port),
			"90",
			h.DBPath+".bak",
			h.DBPath,
		)
		watchdogCmd.SysProcAttr = detachAttr() // platform-specific: detach so systemctl restart can't kill it
		watchdogCmd.Stdout = nil
		watchdogCmd.Stderr = nil
		watchdogCmd.Stdin = nil
		if wdErr := watchdogCmd.Start(); wdErr != nil {
			slog.Warn("update watchdog failed to start; proceeding without rollback safety", "error", wdErr)
		} else {
			slog.Info("update watchdog spawned", "pid", watchdogCmd.Process.Pid, "grace_s", 90)
		}
	}

	// Send the SSE 'complete' event *before* triggering systemctl restart, then
	// give the kernel a chance to flush the bytes to the client. systemd sends
	// SIGTERM almost immediately after `systemctl restart`, so without this
	// pause the SSE consumer sees a connection reset mid-stream and cannot tell
	// success from failure. Mirrors the cluster leave/disband pattern.
	//
	// Branch on supervisor presence: under systemd we ask systemctl to cycle the
	// service; on bare/Docker installs (no /run/systemd/system) systemctl would
	// either be missing or — worse, in a Docker container — talk to the host's
	// systemd. In that case we self-exit with code 0 instead, leaning on the
	// container entrypoint or external supervisor to bring the panel back up.
	// Operators running the binary by hand see the panel stop and must restart
	// it themselves; the SSE message says so.
	if isSystemdActive() {
		if _, err := h.Cmd.Run("systemctl", "is-active", "--quiet", "sfpanel"); err == nil {
			sendEvent("complete", fmt.Sprintf("Updated to v%s. Restarting...", latest))
			go func() {
				time.Sleep(2 * time.Second)
				// Use exec.Command.Start() to restart without blocking — the current process will be replaced.
				_ = exec.Command("systemctl", "restart", "sfpanel").Start()
			}()
			return nil
		}
		// systemd is running but the sfpanel unit isn't active (manual `go run`
		// on a systemd host, or a renamed service). Fall through to the no-supervisor
		// message — we don't know what to ask systemctl to restart.
	}
	// Watchdog (if spawned above) was started with detachAttr/Setsid so it
	// runs in its own session and survives this process exit on Linux —
	// otherwise the no-systemd path would have no rollback if the new binary
	// failed health checks.
	sendEvent("complete", fmt.Sprintf("Updated to v%s. Process is exiting — your supervisor (Docker entrypoint, etc.) must restart sfpanel to load the new binary.", latest))
	go func() {
		time.Sleep(2 * time.Second)
		slog.Info("update complete, exiting for external supervisor restart", "component", "system", "version", latest)
		exitProcess()
	}()
	return nil
}

// clusterUpdateQuorumGuard returns a structured 409 when running this update
// would knock the cluster below Raft quorum. In standalone mode (no manager)
// it's a no-op. The check counts only currently-online voters (heartbeat
// status == StatusOnline); peers already offline are not eligible for the
// survivor count, which matches Raft's own view.
//
// ?force=true bypasses for the operator who genuinely wants to update a
// stranded node without waiting for the cluster to heal.
func (h *Handler) clusterUpdateQuorumGuard(c echo.Context) error {
	if h.ClusterMgr == nil {
		return nil
	}
	if c.QueryParam("force") == "true" {
		return nil
	}
	hb := h.ClusterMgr.GetHeartbeat()
	if hb == nil {
		return nil
	}
	local := h.ClusterMgr.LocalNodeID()
	health := hb.CheckHealth()
	// Total voters = this node + every other online voter. A node whose
	// heartbeat says "offline" or "suspect" is treated as already down,
	// so taking another voter offline must still leave a quorum among
	// the survivors that Raft can see.
	onlineVoters := 1 // ourselves
	for id, status := range health {
		if id == local {
			continue
		}
		if status == cluster.StatusOnline {
			onlineVoters++
		}
	}
	refuse, survivors, quorum := computeUpdateQuorum(onlineVoters)
	if !refuse {
		return nil
	}
	slog.Warn("refusing single-node update — would break quorum",
		"component", "system",
		"local", local,
		"online_voters", onlineVoters,
		"survivors_if_we_leave", survivors,
		"quorum", quorum,
		"remote_ip", c.RealIP())
	return response.Fail(c, http.StatusConflict, response.ErrUpdateInProgress,
		fmt.Sprintf("Refusing update: %d/%d voters would remain online (quorum=%d). Use /cluster/update for a coordinated rolling update, or pass ?force=true to override.", survivors, onlineVoters, quorum))
}

// computeUpdateQuorum returns whether taking one more voter offline would
// drop the cluster below Raft quorum, along with the survivor count and
// quorum threshold for diagnostics. Pulled out as a pure helper so the
// math gets unit coverage without instantiating a real cluster.Manager.
func computeUpdateQuorum(onlineVoters int) (refuse bool, survivors, quorum int) {
	if onlineVoters <= 1 {
		return false, 0, 0
	}
	survivors = onlineVoters - 1
	quorum = onlineVoters/2 + 1
	return survivors < quorum, survivors, quorum
}

// fetchBytes is a small helper that GETs a URL and reads the body into memory.
// Used for the small (<10 KB) signature + cert assets — the archive itself
// goes through io.Copy to a temp file, see the main update path.
func fetchBytes(client *http.Client, url string) ([]byte, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64*1024))
}

// CreateBackup creates a tar.gz archive of DB + config and sends it as download.
func (h *Handler) CreateBackup(c echo.Context) error {
	c.Response().Header().Set("Content-Type", "application/gzip")
	c.Response().Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=sfpanel-backup-%s.tar.gz", time.Now().Format("20060102-150405")))
	c.Response().WriteHeader(http.StatusOK)

	gw := gzip.NewWriter(c.Response())
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	if err := addFileToTar(tw, h.DBPath, "sfpanel.db"); err != nil {
		return err
	}

	if err := addFileToTar(tw, h.ConfigPath, "config.yaml"); err != nil {
		return err
	}

	// Include Docker Compose project files from /opt/stacks/
	if h.ComposePath != "" {
		entries, err := os.ReadDir(h.ComposePath)
		if err == nil {
			composeFiles := []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml", ".env"}
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				for _, cf := range composeFiles {
					filePath := filepath.Join(h.ComposePath, entry.Name(), cf)
					if _, statErr := os.Stat(filePath); statErr == nil {
						archiveName := filepath.Join("compose", entry.Name(), cf)
						if err := addFileToTar(tw, filePath, archiveName); err != nil {
							slog.Warn("backup: skipping file", "path", filePath, "error", err)
						}
					}
				}
			}
		}
	}

	return nil
}

func addFileToTar(tw *tar.Writer, filePath, nameInArchive string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open %s: %w", filePath, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", filePath, err)
	}

	hdr := &tar.Header{
		Name:    nameInArchive,
		Size:    info.Size(),
		Mode:    int64(info.Mode()),
		ModTime: info.ModTime(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}

// RestoreBackup receives a tar.gz upload, validates contents, and restores DB + config.
//
// Hardening notes (2026-05-22):
//   - Concurrent calls refused with 409 via restoreMu (distinct from updateMu).
//   - Tar entries are streamed to per-entry temp files under a staging dir
//     (sibling of the DB) rather than buffered in a map[string][]byte. This
//     keeps RAM bounded on 256 MB cluster nodes that previously OOM-killed
//     when restoring archives with many compose projects.
//   - Compose-file modes preserved from the tar header (with 0o600 default
//     for `.env` / db / config). The old 0o644 hard-code stripped .env's
//     secret-bearing protection.
//   - WAL is checkpointed before the DB file is replaced, so the swap can't
//     leave pending pages stranded against a freshly-renamed inode.
func (h *Handler) RestoreBackup(c echo.Context) error {
	file, err := c.FormFile("backup")
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrRestoreFailed, "No backup file provided")
	}

	src, err := file.Open()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrRestoreFailed, "Failed to open uploaded file")
	}
	defer src.Close()

	gzr, err := gzip.NewReader(src)
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrRestoreFailed, "Invalid gzip file")
	}
	defer gzr.Close()

	// Fix 1: serialise restore. Two concurrent callers would race on .bak/.new
	// filenames and produce inconsistent state. Distinct from updateMu so the
	// 409 is attributable to the restore path specifically.
	if !restoreMu.TryLock() {
		return response.Fail(c, http.StatusConflict, response.ErrRestoreFailed,
			"Another restore is in progress")
	}
	defer restoreMu.Unlock()

	const (
		maxEntrySize int64 = 100 * 1024 * 1024       // per-entry cap (unchanged)
		maxTotalSize int64 = 2 * 1024 * 1024 * 1024  // 2 GiB cumulative cap
	)

	// Fix 2: stream entries to disk in a staging directory rather than
	// buffering every entry in memory. Sibling of the DB so the final
	// os.Rename steps are same-filesystem (and therefore atomic).
	stagingDir, err := os.MkdirTemp(filepath.Dir(h.DBPath), ".sfpanel-restore-*")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrRestoreFailed, "Failed to create staging directory")
	}
	// Best-effort cleanup. The DB/config rename paths move files OUT of
	// this dir; what's left after success is just compose files (already
	// written to their final location via Rename) and any unused entries.
	defer os.RemoveAll(stagingDir)

	type stagedEntry struct {
		relName string      // canonical archive-relative name, e.g. "compose/app/.env"
		path    string      // absolute path under stagingDir
		mode    os.FileMode // sanitised mode bits, see derivation below
	}
	var staged []stagedEntry
	var totalBytes int64

	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return response.Fail(c, http.StatusBadRequest, response.ErrRestoreFailed, "Invalid tar archive")
		}
		if hdr.Typeflag == tar.TypeDir {
			continue
		}
		// Refuse non-regular entries outright. A crafted archive could embed
		// a symlink like "compose/app/docker-compose.yml -> /etc/cron.d/evil";
		// we only want plain files here so later os.WriteFile/Rename calls
		// land exactly where we expect. Modern archive/tar maps the legacy
		// '\x00' regular-file typeflag to TypeReg on read, so this single
		// comparison covers both old and new archive formats.
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		clean := filepath.Clean(hdr.Name)
		if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			continue
		}
		if clean != "sfpanel.db" && clean != "config.yaml" && !strings.HasPrefix(clean, "compose/") {
			continue
		}

		// Stream this entry to a uniquely-named temp file under stagingDir.
		// "/" is replaced with "__" so nested compose paths flatten into a
		// single directory level — we keep the original relName on the side
		// for the final Rename destination calculation.
		safeName := strings.ReplaceAll(clean, "/", "__")
		tmpPath := filepath.Join(stagingDir, safeName)
		f, openErr := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if openErr != nil {
			return response.Fail(c, http.StatusInternalServerError, response.ErrRestoreFailed, "Failed to create staging file")
		}
		// LimitReader at maxEntrySize+1 so a written count > maxEntrySize
		// proves the entry exceeded the cap (LimitReader at exactly the cap
		// would not let us distinguish "exactly cap" from "overflowed").
		written, copyErr := io.Copy(f, io.LimitReader(tr, maxEntrySize+1))
		_ = f.Close()
		if copyErr != nil {
			return response.Fail(c, http.StatusInternalServerError, response.ErrRestoreFailed, "Failed to read archive entry")
		}
		if written > maxEntrySize {
			return response.Fail(c, http.StatusBadRequest, response.ErrRestoreFailed, "Archive entry exceeds size cap")
		}
		totalBytes += written
		if totalBytes > maxTotalSize {
			return response.Fail(c, http.StatusBadRequest, response.ErrRestoreFailed, "Archive total size exceeds cap")
		}

		// Fix 3: derive the destination mode from the tar header rather than
		// hard-coding 0o644. Tar's Mode field is the POSIX mode; we mask to
		// the perm bits and fall back to a secure default if the header
		// omitted it (some tar producers write Mode=0 for "use defaults").
		mode := os.FileMode(hdr.Mode & 0o777)
		if mode == 0 {
			if strings.HasSuffix(clean, ".env") || clean == "sfpanel.db" || clean == "config.yaml" {
				mode = 0o600
			} else {
				mode = 0o644
			}
		}
		staged = append(staged, stagedEntry{relName: clean, path: tmpPath, mode: mode})
	}

	// Build a lookup so the rename-by-name steps below stay readable.
	stagedByName := make(map[string]stagedEntry, len(staged))
	for _, e := range staged {
		stagedByName[e.relName] = e
	}

	if _, ok := stagedByName["sfpanel.db"]; !ok {
		return response.Fail(c, http.StatusBadRequest, response.ErrRestoreFailed, "Backup must contain sfpanel.db")
	}

	// Create backups of current files (copy via ReadFile is fine — these are
	// small; the staged uploads above are what we worry about for RAM).
	if data, readErr := os.ReadFile(h.DBPath); readErr == nil {
		if err := os.WriteFile(h.DBPath+".bak", data, 0o600); err != nil {
			return response.Fail(c, http.StatusInternalServerError, response.ErrRestoreFailed, "Failed to create database backup")
		}
	}
	if data, readErr := os.ReadFile(h.ConfigPath); readErr == nil {
		if err := os.WriteFile(h.ConfigPath+".bak", data, 0o600); err != nil {
			return response.Fail(c, http.StatusInternalServerError, response.ErrRestoreFailed, "Failed to create config backup")
		}
	}

	// Fix 4: force WAL pages back into the main DB file before we yank the
	// inode out from under the live *sql.DB handle. Without this, any
	// uncommitted-to-main pages in sfpanel.db-wal would be paired with the
	// FRESH inode after rename — which SQLite would either treat as
	// corruption or, worse, silently apply WAL frames to the wrong DB.
	//
	// Nil-safe for the unit-test handler, which has no real DB connection.
	// Best-effort: if the checkpoint fails we proceed anyway — the next
	// process startup will rebuild WAL state from the restored DB, and
	// the existing -wal file will be discarded as belonging to a different
	// generation of the database.
	if h.DB != nil {
		if _, cpErr := h.DB.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); cpErr != nil {
			slog.Warn("wal checkpoint before restore failed (continuing)",
				"component", "system", "err", cpErr)
		}
	}

	// Rename the staged DB into place. Same-filesystem rename is atomic.
	dbEntry := stagedByName["sfpanel.db"]
	dbNewPath := h.DBPath + ".new"
	if err := os.Rename(dbEntry.path, dbNewPath); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrRestoreFailed, "Failed to stage database")
	}
	// DB is always 0o600 regardless of header mode.
	_ = os.Chmod(dbNewPath, 0o600)
	if err := os.Rename(dbNewPath, h.DBPath); err != nil {
		_ = os.Remove(dbNewPath)
		return response.Fail(c, http.StatusInternalServerError, response.ErrRestoreFailed, "Failed to restore database")
	}

	if cfgEntry, ok := stagedByName["config.yaml"]; ok {
		cfgNewPath := h.ConfigPath + ".new"
		if err := os.Rename(cfgEntry.path, cfgNewPath); err != nil {
			// Rollback DB to the .bak we made above.
			if bakData, bakErr := os.ReadFile(h.DBPath + ".bak"); bakErr == nil {
				_ = os.WriteFile(h.DBPath, bakData, 0o600)
			}
			return response.Fail(c, http.StatusInternalServerError, response.ErrRestoreFailed, "Failed to stage config")
		}
		_ = os.Chmod(cfgNewPath, 0o600)
		if err := os.Rename(cfgNewPath, h.ConfigPath); err != nil {
			_ = os.Remove(cfgNewPath)
			if bakData, bakErr := os.ReadFile(h.DBPath + ".bak"); bakErr == nil {
				_ = os.WriteFile(h.DBPath, bakData, 0o600)
			}
			return response.Fail(c, http.StatusInternalServerError, response.ErrRestoreFailed, "Failed to restore config")
		}
	}

	if h.ComposePath != "" {
		composePath := filepath.Clean(h.ComposePath)
		for _, e := range staged {
			if !strings.HasPrefix(e.relName, "compose/") {
				continue
			}
			relPath := strings.TrimPrefix(e.relName, "compose/")
			destPath := filepath.Join(composePath, relPath)
			if !strings.HasPrefix(filepath.Clean(destPath), composePath+string(os.PathSeparator)) {
				continue
			}
			destDir := filepath.Dir(destPath)
			if err := os.MkdirAll(destDir, 0o755); err != nil {
				continue
			}
			// Rename from staging (same FS) into the destination, then chmod
			// to the header-derived mode. Rename over an existing file is
			// atomic on POSIX, so an interrupted restore can't leave a
			// half-written compose file in place.
			if err := os.Rename(e.path, destPath); err != nil {
				continue
			}
			_ = os.Chmod(destPath, e.mode)
		}
	}

	// Restart strategy mirrors RunUpdate: under systemd, ask systemctl to cycle
	// the unit so the new DB/config are loaded with a fresh connection pool;
	// elsewhere, exit so the container entrypoint / external supervisor can
	// bring us back up. Continuing to serve with the old *sql.DB handle pointed
	// at a freshly-overwritten file is undefined-behaviour territory in SQLite —
	// better to terminate than to corrupt.
	if isSystemdActive() {
		if _, err := h.Cmd.Run("systemctl", "is-active", "--quiet", "sfpanel"); err == nil {
			// Use exec.Command.Start() to restart without blocking — the current process will be replaced.
			_ = exec.Command("systemctl", "restart", "sfpanel").Start()
			return response.OK(c, map[string]string{"message": "Backup restored. Service restarting..."})
		}
	}

	// No supervisor we can drive: schedule self-exit after the response flushes
	// so the operator's HTTP client sees the success payload before the socket
	// drops. The user-facing message is explicit that the process is going away.
	go func() {
		time.Sleep(2 * time.Second)
		slog.Info("backup restored, exiting for external supervisor restart", "component", "system")
		exitProcess()
	}()
	return response.OK(c, map[string]string{"message": "Backup restored. The panel process is exiting — your supervisor (Docker entrypoint, etc.) must restart sfpanel to load the new database."})
}
