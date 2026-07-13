// update.go — self-update mechanism for datastar-lint.
//
// On startup the tool checks the GitHub Releases API for a newer version.
// If one is found a short message is printed to stderr.
// The --update flag downloads the latest archive, extracts the binary,
// and atomically replaces the running executable.
//
// The asset-naming convention mirrors the goreleaser config file:
//
//	datastar-lint_{Version}_macOS_arm64.tar.gz
//	datastar-lint_{Version}_macOS_x86_64.tar.gz
//	datastar-lint_{Version}_linux_x86_64.tar.gz
package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	githubOwner = "calionauta"
	githubRepo  = "datastar-lint"
)

// osExecutable is overridden in tests to control which binary gets replaced.
var osExecutable = os.Executable

// ---------------------------------------------------------------------------
// GitHub API types (minimal)
// ---------------------------------------------------------------------------

type ghRelease struct {
	TagName string     `json:"tag_name"`
	Assets  []ghAsset  `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// CheckForUpdate fetches the latest release tag from GitHub and compares it
// with the current version. Returns a user-facing message if a newer version
// is available, or the empty string when already up-to-date (or on error).
//
// The underlying HTTP request has its own timeout so it will never block the
// main lint longer than expected.
func CheckForUpdate(timeout time.Duration) string {
	release, err := fetchLatestRelease(timeout)
	if err != nil {
		return ""
	}

	latestTag := release.TagName
	currentTag := "v" + strings.TrimPrefix(version, "v")

	if !isNewerVersion(currentTag, latestTag) {
		return ""
	}

	// Show major.minor.patch without leading 'v' for brevity.
	cur := strings.TrimPrefix(currentTag, "v")
	lat := strings.TrimPrefix(latestTag, "v")

	return fmt.Sprintf("── ⋆ ──\n"+
		"  Update available: %s → %s\n"+
		"  Run  datastar-lint --update  to upgrade automatically,\n"+
		"  or   go install %s/%s@latest\n",
		cur, lat, githubOwner, githubRepo)
}

// CheckForUpdateFromURL is like CheckForUpdate but uses a custom API URL
// (used in tests with httptest.Server).
func CheckForUpdateFromURL(apiURL string, timeout time.Duration) string {
	release, err := fetchReleaseFromURL(apiURL, timeout)
	if err != nil {
		return ""
	}

	latestTag := release.TagName
	currentTag := "v" + strings.TrimPrefix(version, "v")

	if !isNewerVersion(currentTag, latestTag) {
		return ""
	}

	cur := strings.TrimPrefix(currentTag, "v")
	lat := strings.TrimPrefix(latestTag, "v")

	return fmt.Sprintf("── ⋆ ──\n"+
		"  Update available: %s → %s\n"+
		"  Run  datastar-lint --update  to upgrade automatically,\n"+
		"  or   go install %s/%s@latest\n",
		cur, lat, githubOwner, githubRepo)
}

// SelfUpdate downloads the archive for the current platform from the latest
// GitHub release, extracts the binary, and atomically replaces the running
// executable.
func SelfUpdate() error {
	release, err := fetchLatestRelease(30 * time.Second)
	if err != nil {
		return fmt.Errorf("check for updates: %w", err)
	}

	latestTag := release.TagName
	currentTag := "v" + strings.TrimPrefix(version, "v")
	if !isNewerVersion(currentTag, latestTag) {
		return fmt.Errorf("already up-to-date (v%s)", strings.TrimPrefix(version, "v"))
	}

	// Find the matching asset.
	suffix := archiveSuffix()
	var downloadURL string
	for _, a := range release.Assets {
		if strings.Contains(a.Name, suffix) {
			downloadURL = a.BrowserDownloadURL
			break
		}
	}
	if downloadURL == "" {
		return fmt.Errorf("no release asset found for %s/%s (expected suffix %q)",
			runtime.GOOS, runtime.GOARCH, suffix)
	}

	// Download the archive.
	fmt.Fprintf(os.Stderr, "Downloading %s …\n", release.TagName)
	bin, err := downloadBinary(downloadURL)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}

	// Atomically replace the running binary.
	if err := atomicReplace(bin); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Updated to datastar-lint %s\n", strings.TrimPrefix(latestTag, "v"))
	return nil
}

// SelfUpdateFromURL is like SelfUpdate but uses custom URLs for API and
// download (used in tests with httptest.Server).
func SelfUpdateFromURL(apiURL, downloadBaseURL string, timeout time.Duration) error {
	release, err := fetchReleaseFromURL(apiURL, timeout)
	if err != nil {
		return fmt.Errorf("check for updates: %w", err)
	}

	latestTag := release.TagName
	currentTag := "v" + strings.TrimPrefix(version, "v")
	if !isNewerVersion(currentTag, latestTag) {
		return fmt.Errorf("already up-to-date (v%s)", strings.TrimPrefix(version, "v"))
	}

	suffix := archiveSuffix()
	var downloadURL string
	for _, a := range release.Assets {
		if strings.Contains(a.Name, suffix) {
			downloadURL = a.BrowserDownloadURL
			break
		}
	}
	if downloadURL == "" {
		return fmt.Errorf("no release asset found for %s/%s (expected suffix %q)",
			runtime.GOOS, runtime.GOARCH, suffix)
	}

	fmt.Fprintf(os.Stderr, "Downloading %s …\n", release.TagName)
	bin, err := downloadBinaryFromURL(downloadURL, &http.Client{Timeout: timeout})
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}

	if err := atomicReplace(bin); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Updated to datastar-lint %s\n", strings.TrimPrefix(latestTag, "v"))
	return nil
}

// ---------------------------------------------------------------------------
// Version comparison
// ---------------------------------------------------------------------------

func parseVersion(v string) (major, minor, patch int, ok bool) {
	v = strings.TrimPrefix(v, "v")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, 0, false
	}
	minor, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, 0, false
	}
	patch, err = strconv.Atoi(parts[2])
	if err != nil {
		return 0, 0, 0, false
	}
	return major, minor, patch, true
}

func isNewerVersion(current, latest string) bool {
	cmaj, cmin, cpatch, cok := parseVersion(current)
	lmaj, lmin, lpatch, lok := parseVersion(latest)
	if !cok || !lok {
		// Fall back to simple string comparison.
		return strings.TrimPrefix(latest, "v") > strings.TrimPrefix(current, "v")
	}
	switch {
	case lmaj > cmaj:
		return true
	case lmaj < cmaj:
		return false
	case lmin > cmin:
		return true
	case lmin < cmin:
		return false
	default:
		return lpatch > cpatch
	}
}

// ---------------------------------------------------------------------------
// Asset matching
// ---------------------------------------------------------------------------

// archiveSuffix returns the platform-specific suffix used in goreleaser
// archive names, e.g. "macOS_arm64" or "linux_x86_64".
func archiveSuffix() string {
	osName := runtime.GOOS
	if osName == "darwin" {
		osName = "macOS"
	}
	archName := runtime.GOARCH
	if archName == "amd64" {
		archName = "x86_64"
	}
	return fmt.Sprintf("_%s_%s", osName, archName)
}

// ---------------------------------------------------------------------------
// GitHub API call
// ---------------------------------------------------------------------------

func fetchLatestRelease(timeout time.Duration) (*ghRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest",
		githubOwner, githubRepo)
	return fetchReleaseFromURL(url, timeout)
}

func fetchReleaseFromURL(url string, timeout time.Duration) (*ghRelease, error) {
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "datastar-lint/"+version)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned HTTP %d", resp.StatusCode)
	}

	var release ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, err
	}
	return &release, nil
}

// ---------------------------------------------------------------------------
// Download + extraction
// ---------------------------------------------------------------------------

// downloadBinary downloads a tar.gz archive from url and extracts the embedded
// executable binary, returning its content.
func downloadBinary(url string) ([]byte, error) {
	return downloadBinaryFromURL(url, &http.Client{Timeout: 60 * time.Second})
}

func downloadBinaryFromURL(url string, client *http.Client) ([]byte, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned HTTP %d", resp.StatusCode)
	}

	gzr, err := gzip.NewReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	defer gzr.Close()

	// Archive name for the binary inside the tar.gz (set by GoReleaser).
	const binName = "datastar-lint"

	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("tar: %w", err)
		}

		if hdr.Typeflag != tar.TypeReg || hdr.Name != binName {
			continue
		}

		dat, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", hdr.Name, err)
		}
		return dat, nil
	}

	return nil, fmt.Errorf("no executable found in archive")
}

// ---------------------------------------------------------------------------
// Atomic replacement
// ---------------------------------------------------------------------------

// atomicReplace replaces the running executable with new content.
// It writes to a sibling temp file, renames the old binary to a backup, and
// then renames the new file in place. On success the backup is removed.
func atomicReplace(newBin []byte) error {
	exe, err := osExecutable()
	if err != nil {
		return fmt.Errorf("cannot determine executable path: %w", err)
	}

	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("cannot resolve symlinks: %w", err)
	}

	dir := filepath.Dir(exe)

	// Write new binary to a temp file.
	tmp, err := os.CreateTemp(dir, ".datastar-lint-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(newBin); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write temp binary: %w", err)
	}
	if err := tmp.Chmod(0755); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("chmod temp binary: %w", err)
	}
	tmp.Close()

	backupPath := exe + ".old"

	// Rename current binary to backup.
	if err := os.Rename(exe, backupPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename existing binary: %w", err)
	}

	// Move temp file into place.
	if err := os.Rename(tmpPath, exe); err != nil {
		// Restore backup.
		os.Rename(backupPath, exe)
		os.Remove(tmpPath)
		return fmt.Errorf("rename new binary: %w", err)
	}

	// Clean up backup.
	os.Remove(backupPath)

	return nil
}
