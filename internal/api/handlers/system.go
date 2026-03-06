package handlers

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
)

type SystemHandler struct {
	Version     string
	DBPath      string
	ConfigPath  string
	ComposePath string
}

type GitHubRelease struct {
	TagName     string `json:"tag_name"`
	Body        string `json:"body"`
	PublishedAt string `json:"published_at"`
}

type UpdateCheckResponse struct {
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version"`
	UpdateAvailable bool   `json:"update_available"`
	ReleaseNotes    string `json:"release_notes"`
	PublishedAt     string `json:"published_at"`
}

// CheckUpdate queries GitHub releases API and returns version comparison.
func (h *SystemHandler) CheckUpdate(c echo.Context) error {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get("https://api.github.com/repos/svrforum/SFPanel/releases/latest")
	if err != nil {
		return response.Fail(c, http.StatusBadGateway, response.ErrUpdateCheckFailed, "Failed to check for updates")
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return response.Fail(c, http.StatusBadGateway, response.ErrUpdateCheckFailed,
			fmt.Sprintf("GitHub API returned %d", resp.StatusCode))
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrUpdateCheckFailed, "Failed to parse release info")
	}

	latest := strings.TrimPrefix(release.TagName, "v")
	return response.OK(c, UpdateCheckResponse{
		CurrentVersion:  h.Version,
		LatestVersion:   latest,
		UpdateAvailable: latest != h.Version,
		ReleaseNotes:    release.Body,
		PublishedAt:     release.PublishedAt,
	})
}

// RunUpdate downloads the latest release and replaces the current binary, streaming progress via SSE.
func (h *SystemHandler) RunUpdate(c echo.Context) error {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get("https://api.github.com/repos/svrforum/SFPanel/releases/latest")
	if err != nil {
		return response.Fail(c, http.StatusBadGateway, response.ErrUpdateFailed, "Failed to check for updates")
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return response.Fail(c, http.StatusBadGateway, response.ErrUpdateFailed, "GitHub API error")
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrUpdateFailed, "Failed to parse release")
	}
	latest := strings.TrimPrefix(release.TagName, "v")
	if latest == h.Version {
		return response.OK(c, map[string]string{"status": "up_to_date"})
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
	url := fmt.Sprintf("https://github.com/svrforum/SFPanel/releases/download/v%s/sfpanel_%s_linux_%s.tar.gz", latest, latest, arch)
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

	// Extract
	sendEvent("extracting", "Extracting binary...")
	gzr, err := gzip.NewReader(dlResp.Body)
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
			binaryData, err = io.ReadAll(tr)
			if err != nil {
				sendEvent("error", fmt.Sprintf("Binary read failed: %v", err))
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
	execPath, err := os.Executable()
	if err != nil {
		sendEvent("error", fmt.Sprintf("Cannot find binary path: %v", err))
		return nil
	}

	// Backup current binary
	backupPath := execPath + ".bak"
	if data, readErr := os.ReadFile(execPath); readErr == nil {
		_ = os.WriteFile(backupPath, data, 0755)
	}

	// Backup DB + config before update
	if data, readErr := os.ReadFile(h.DBPath); readErr == nil {
		_ = os.WriteFile(h.DBPath+".bak", data, 0644)
	}
	if data, readErr := os.ReadFile(h.ConfigPath); readErr == nil {
		_ = os.WriteFile(h.ConfigPath+".bak", data, 0644)
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
	if err := exec.Command("systemctl", "is-active", "--quiet", "sfpanel").Run(); err == nil {
		_ = exec.Command("systemctl", "restart", "sfpanel").Start()
	}

	sendEvent("complete", fmt.Sprintf("Updated to v%s. Restarting...", latest))
	return nil
}

// CreateBackup creates a tar.gz archive of DB + config and sends it as download.
func (h *SystemHandler) CreateBackup(c echo.Context) error {
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
						_ = addFileToTar(tw, filePath, archiveName)
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
		Name: nameInArchive,
		Size: info.Size(),
		Mode: int64(info.Mode()),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}

// RestoreBackup receives a tar.gz upload, validates contents, and restores DB + config.
func (h *SystemHandler) RestoreBackup(c echo.Context) error {
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

	tr := tar.NewReader(gzr)
	files := make(map[string][]byte)
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
		if hdr.Name == "sfpanel.db" || hdr.Name == "config.yaml" || strings.HasPrefix(hdr.Name, "compose/") {
			data, readErr := io.ReadAll(tr)
			if readErr != nil {
				return response.Fail(c, http.StatusInternalServerError, response.ErrRestoreFailed, "Failed to read archive entry")
			}
			files[hdr.Name] = data
		}
	}

	if _, ok := files["sfpanel.db"]; !ok {
		return response.Fail(c, http.StatusBadRequest, response.ErrRestoreFailed, "Backup must contain sfpanel.db")
	}

	// Backup current files
	if data, readErr := os.ReadFile(h.DBPath); readErr == nil {
		_ = os.WriteFile(h.DBPath+".bak", data, 0644)
	}
	if data, readErr := os.ReadFile(h.ConfigPath); readErr == nil {
		_ = os.WriteFile(h.ConfigPath+".bak", data, 0644)
	}

	// Write restored files
	if dbData, ok := files["sfpanel.db"]; ok {
		if err := os.WriteFile(h.DBPath, dbData, 0644); err != nil {
			return response.Fail(c, http.StatusInternalServerError, response.ErrRestoreFailed, "Failed to restore database")
		}
	}
	if cfgData, ok := files["config.yaml"]; ok {
		if err := os.WriteFile(h.ConfigPath, cfgData, 0644); err != nil {
			return response.Fail(c, http.StatusInternalServerError, response.ErrRestoreFailed, "Failed to restore config")
		}
	}

	// Restore compose files
	if h.ComposePath != "" {
		for name, data := range files {
			if !strings.HasPrefix(name, "compose/") {
				continue
			}
			// name format: compose/<project>/<filename>
			relPath := strings.TrimPrefix(name, "compose/")
			destPath := filepath.Join(h.ComposePath, relPath)
			destDir := filepath.Dir(destPath)
			if err := os.MkdirAll(destDir, 0755); err != nil {
				continue
			}
			_ = os.WriteFile(destPath, data, 0644)
		}
	}

	// Restart service
	if err := exec.Command("systemctl", "is-active", "--quiet", "sfpanel").Run(); err == nil {
		_ = exec.Command("systemctl", "restart", "sfpanel").Start()
	}

	return response.OK(c, map[string]string{"message": "Backup restored. Service restarting..."})
}
