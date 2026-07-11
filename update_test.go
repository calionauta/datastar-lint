package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --------------- parseVersion ---------------

func TestParseVersion(t *testing.T) {
	tests := []struct {
		input string
		major int
		minor int
		patch int
		ok    bool
	}{
		{"v0.8.0", 0, 8, 0, true},
		{"1.2.3", 1, 2, 3, true},
		{"v10.20.30", 10, 20, 30, true},
		{"", 0, 0, 0, false},
		{"v1.2", 0, 0, 0, false},
		{"v1.2.3.4", 0, 0, 0, false},
		{"abc", 0, 0, 0, false},
		{"v1.x.3", 0, 0, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			major, minor, patch, ok := parseVersion(tc.input)
			if major != tc.major || minor != tc.minor || patch != tc.patch || ok != tc.ok {
				t.Errorf("parseVersion(%q) = (%d,%d,%d,%v), want (%d,%d,%d,%v)",
					tc.input, major, minor, patch, ok, tc.major, tc.minor, tc.patch, tc.ok)
			}
		})
	}
}

func TestIsNewerVersion(t *testing.T) {
	tests := []struct {
		current string
		latest  string
		want    bool
	}{
		{"v0.8.0", "v0.9.0", true},
		{"v0.8.0", "v0.8.1", true},
		{"v0.8.0", "v1.0.0", true},
		{"v0.8.0", "v0.8.0", false},
		{"v0.9.0", "v0.8.0", false},
		{"v1.0.0", "v0.9.0", false},
		{"v0.8.0", "v0.7.0", false},
		{"v0.8.0", "v0.8.0-beta", true},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("%s vs %s", tc.current, tc.latest), func(t *testing.T) {
			got := isNewerVersion(tc.current, tc.latest)
			if got != tc.want {
				t.Errorf("isNewerVersion(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.want)
			}
		})
	}
}

func TestArchiveSuffix(t *testing.T) {
	suffix := archiveSuffix()
	if !strings.HasPrefix(suffix, "_") {
		t.Errorf("archiveSuffix should start with underscore, got %q", suffix)
	}
}

// --------------- HTTP-dependent tests ---------------

func TestFetchReleaseFromURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/vnd.github.v3+json" {
			t.Error("expected Accept header")
		}
		if r.Header.Get("User-Agent") == "" {
			t.Error("expected User-Agent header")
		}
		resp := ghRelease{
			TagName: "v0.9.0",
			Assets: []ghAsset{
				{Name: "datastar-lint_v0.9.0_macOS_arm64.tar.gz", BrowserDownloadURL: "https://example.com/macos.tar.gz"},
			},
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	release, err := fetchReleaseFromURL(srv.URL, 5*time.Second)
	if err != nil {
		t.Fatalf("fetchReleaseFromURL: %v", err)
	}
	if release.TagName != "v0.9.0" {
		t.Errorf("expected tag v0.9.0, got %q", release.TagName)
	}
	if len(release.Assets) != 1 {
		t.Fatalf("expected 1 asset, got %d", len(release.Assets))
	}
	if release.Assets[0].Name != "datastar-lint_v0.9.0_macOS_arm64.tar.gz" {
		t.Errorf("unexpected asset name: %s", release.Assets[0].Name)
	}
}

func TestFetchReleaseFromURL_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := fetchReleaseFromURL(srv.URL, 5*time.Second)
	if err == nil {
		t.Fatal("expected error for 404, got nil")
	}
}

func TestDownloadBinaryFromURL(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	content := []byte("#!/bin/sh\necho fake binary")
	hdr := &tar.Header{
		Name: "datastar-lint",
		Size: int64(len(content)),
		Mode: 0755,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(buf.Bytes())
	}))
	defer srv.Close()

	bin, err := downloadBinaryFromURL(srv.URL, &http.Client{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("downloadBinaryFromURL: %v", err)
	}
	if !bytes.Equal(bin, content) {
		t.Errorf("downloaded binary content mismatch")
	}
}

func TestDownloadBinaryFromURL_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := downloadBinaryFromURL(srv.URL, &http.Client{Timeout: 5 * time.Second})
	if err == nil {
		t.Fatal("expected error for 404, got nil")
	}
}

func TestDownloadBinaryFromURL_EmptyArchive(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	gz.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(buf.Bytes())
	}))
	defer srv.Close()

	_, err := downloadBinaryFromURL(srv.URL, &http.Client{Timeout: 5 * time.Second})
	if err == nil {
		t.Fatal("expected error for empty archive, got nil")
	}
}

func TestCheckForUpdate_NewerAvailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ghRelease{TagName: "v9.9.9"}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	msg := CheckForUpdateFromURL(srv.URL, 5*time.Second)
	if msg == "" {
		t.Fatal("expected update message, got empty")
	}
	if !strings.Contains(msg, "9.9.9") {
		t.Errorf("expected message to mention 9.9.9, got %q", msg)
	}
}

func TestCheckForUpdate_UpToDate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ghRelease{TagName: "v0.8.0"}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	msg := CheckForUpdateFromURL(srv.URL, 5*time.Second)
	if msg != "" {
		t.Errorf("expected empty message for same version, got %q", msg)
	}
}

func TestCheckForUpdate_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	msg := CheckForUpdateFromURL(srv.URL, 5*time.Second)
	if msg != "" {
		t.Errorf("expected empty message on HTTP error, got %q", msg)
	}
}

func TestCheckForUpdate_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
	}))
	defer srv.Close()

	msg := CheckForUpdateFromURL(srv.URL, 1*time.Millisecond)
	if msg != "" {
		t.Errorf("expected empty message on timeout, got %q", msg)
	}
}

func TestSelfUpdate_Success(t *testing.T) {
	content := []byte("new binary content")
	archive := buildTarGz(t, content)

	var downloadURL string
	suff := archiveSuffix()
	apiCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalls++
		if apiCalls == 1 {
			resp := ghRelease{
				TagName: "v9.9.9",
				Assets: []ghAsset{
					{Name: "datastar-lint_v9.9.9" + suff + ".tar.gz", BrowserDownloadURL: downloadURL},
				},
			}
			json.NewEncoder(w).Encode(resp)
			return
		}
		w.Write(archive)
	}))
	defer srv.Close()
	downloadURL = srv.URL + "/download"

	tmpDir := t.TempDir()
	oldExe := filepath.Join(tmpDir, "datastar-lint")
	if err := os.WriteFile(oldExe, []byte("old binary"), 0755); err != nil {
		t.Fatal(err)
	}

	origExec := osExecutable
	osExecutable = func() (string, error) { return oldExe, nil }
	defer func() { osExecutable = origExec }()

	err := SelfUpdateFromURL(srv.URL, srv.URL, 5*time.Second)
	if err != nil {
		t.Fatalf("SelfUpdate: %v", err)
	}

	got, err := os.ReadFile(oldExe)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("binary content mismatch: got %q, want %q", got, content)
	}

	if _, err := os.Stat(oldExe + ".old"); !os.IsNotExist(err) {
		t.Errorf("backup file should have been removed")
	}
}

func TestSelfUpdate_AlreadyUpToDate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ghRelease{TagName: "v0.8.0"}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	err := SelfUpdateFromURL(srv.URL, srv.URL, 5*time.Second)
	if err == nil {
		t.Fatal("expected error for already up-to-date, got nil")
	}
	if !strings.Contains(err.Error(), "already up-to-date") {
		t.Errorf("expected 'already up-to-date' error, got %v", err)
	}
}

func TestSelfUpdate_NoMatchingAsset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ghRelease{
			TagName: "v9.9.9",
			Assets: []ghAsset{
				{Name: "datastar-lint_v9.9.9_nonexistent_platform.tar.gz", BrowserDownloadURL: "https://example.com/nope.tar.gz"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	err := SelfUpdateFromURL(srv.URL, srv.URL, 5*time.Second)
	if err == nil {
		t.Fatal("expected error for no matching asset, got nil")
	}
	if !strings.Contains(err.Error(), "no release asset found") {
		t.Errorf("expected 'no release asset found' error, got %v", err)
	}
}

func TestAtomicReplace(t *testing.T) {
	tmpDir := t.TempDir()
	exePath := filepath.Join(tmpDir, "datastar-lint")
	if err := os.WriteFile(exePath, []byte("old binary"), 0755); err != nil {
		t.Fatal(err)
	}

	origExec := osExecutable
	osExecutable = func() (string, error) { return exePath, nil }
	defer func() { osExecutable = origExec }()

	newContent := []byte("new binary content")
	if err := atomicReplace(newContent); err != nil {
		t.Fatalf("atomicReplace: %v", err)
	}

	got, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, newContent) {
		t.Errorf("content mismatch: got %q, want %q", got, newContent)
	}

	if _, err := os.Stat(exePath + ".old"); !os.IsNotExist(err) {
		t.Errorf("backup file should have been removed")
	}
}

func TestAtomicReplace_WriteError(t *testing.T) {
	tmpDir := t.TempDir()
	exePath := filepath.Join(tmpDir, "datastar-lint")
	if err := os.WriteFile(exePath, []byte("old"), 0444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(tmpDir, 0444); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(tmpDir, 0755)

	origExec := osExecutable
	osExecutable = func() (string, error) { return exePath, nil }
	defer func() { osExecutable = origExec }()

	err := atomicReplace([]byte("new content"))
	if err == nil {
		t.Error("expected error for read-only directory, got nil")
	}
}

// --------------- Helpers ---------------

func buildTarGz(t *testing.T, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{
		Name: "datastar-lint",
		Size: int64(len(content)),
		Mode: 0755,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}