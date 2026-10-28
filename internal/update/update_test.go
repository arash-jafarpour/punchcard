package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// archiveFor builds a release archive that holds the given binary content.
func archiveFor(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer

	if strings.HasSuffix(name, ".zip") {
		zw := zip.NewWriter(&buf)
		w, err := zw.Create(binaryName)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}

	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	// A directory entry and an extra file must not disturb the search.
	if err := tw.WriteHeader(&tar.Header{Name: "README.md", Mode: 0o644, Size: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("hi")); err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: binaryName, Mode: 0o755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// releaseServer serves one archive and its checksum sidecar. When badSum is
// true, the sidecar holds a digest that does not match the archive.
func releaseServer(t *testing.T, archive []byte, badSum bool) (srv *httptest.Server, assetName string) {
	t.Helper()

	assetName = "punchcard_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	if runtime.GOOS == "windows" {
		assetName = strings.TrimSuffix(assetName, ".tar.gz") + ".zip"
	}

	digest := sha256.Sum256(archive)
	sum := hex.EncodeToString(digest[:])
	if badSum {
		sum = strings.Repeat("0", 64)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/name/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		body := `{"tag_name":"v9.9.9","body":"## Features\n- new things","assets":[`
		body += `{"name":"` + assetName + `","size":` + strconv.Itoa(len(archive)) + `,"browser_download_url":"` + srv.URL + `/asset"},`
		body += `{"name":"` + assetName + `.sha256","browser_download_url":"` + srv.URL + `/sum"}]}`
		_, _ = w.Write([]byte(body))
	})
	mux.HandleFunc("/asset", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	})
	mux.HandleFunc("/sum", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(sum + "  " + assetName + "\n"))
	})

	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, assetName
}

// tempExe returns a file that stands in for the running binary.
func tempExe(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "punchcard")
	if err := os.WriteFile(path, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunInstallsTheNewBinary(t *testing.T) {
	want := []byte("new binary contents")
	archive := archiveFor(t, "punchcard_"+runtime.GOOS+"_"+runtime.GOARCH+".tar.gz", want)
	if runtime.GOOS == "windows" {
		archive = archiveFor(t, "punchcard_windows_amd64.zip", want)
	}
	srv, _ := releaseServer(t, archive, false)
	exe := tempExe(t)

	var out bytes.Buffer
	err := Run(context.Background(), Options{
		CurrentVersion: "0.1.0",
		RepoSlug:       "owner/name",
		APIBase:        srv.URL,
		Exe:            exe,
		Out:            &out,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("binary = %q, want %q", got, want)
	}
	info, err := os.Stat(exe)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %o, want 755", info.Mode().Perm())
	}
	if !strings.Contains(out.String(), "successfully updated punchcard to v9.9.9") {
		t.Errorf("output = %q, want a success line", out.String())
	}
	if !strings.Contains(out.String(), "new things") {
		t.Errorf("output = %q, want the release notes", out.String())
	}
}

func TestRunKeepsTheOldBinaryOnBadChecksum(t *testing.T) {
	archive := archiveFor(t, "punchcard_"+runtime.GOOS+"_"+runtime.GOARCH+".tar.gz", []byte("evil"))
	srv, _ := releaseServer(t, archive, true)
	exe := tempExe(t)

	err := Run(context.Background(), Options{
		CurrentVersion: "0.1.0",
		RepoSlug:       "owner/name",
		APIBase:        srv.URL,
		Exe:            exe,
		Out:            &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("error = %v, want a checksum error", err)
	}

	got, readErr := os.ReadFile(exe)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "old binary" {
		t.Errorf("binary = %q, want the old contents", got)
	}
	// No staging file may stay behind.
	entries, err := os.ReadDir(filepath.Dir(exe))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".punchcard-update-") {
			t.Errorf("staging file left behind: %s", entry.Name())
		}
	}
}

func TestRunCheckOnly(t *testing.T) {
	archive := archiveFor(t, "punchcard_"+runtime.GOOS+"_"+runtime.GOARCH+".tar.gz", []byte("new"))
	srv, _ := releaseServer(t, archive, false)
	exe := tempExe(t)

	var out bytes.Buffer
	err := Run(context.Background(), Options{
		CurrentVersion: "0.1.0",
		RepoSlug:       "owner/name",
		APIBase:        srv.URL,
		Exe:            exe,
		Out:            &out,
		CheckOnly:      true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "new version available: v9.9.9") {
		t.Errorf("output = %q", out.String())
	}

	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old binary" {
		t.Error("--check replaced the binary")
	}
}

func TestRunAlreadyUpToDate(t *testing.T) {
	srv, _ := releaseServer(t, nil, false)

	var out bytes.Buffer
	err := Run(context.Background(), Options{
		CurrentVersion: "9.9.9",
		RepoSlug:       "owner/name",
		APIBase:        srv.URL,
		Exe:            tempExe(t),
		Out:            &out,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "already up to date") {
		t.Errorf("output = %q", out.String())
	}
}

func TestRunRejectsDevelopmentBuild(t *testing.T) {
	for _, version := range []string{"dev", "unknown", ""} {
		err := Run(context.Background(), Options{
			CurrentVersion: version,
			RepoSlug:       "owner/name",
			Out:            &bytes.Buffer{},
		})
		if err == nil || !strings.Contains(err.Error(), "development build") {
			t.Errorf("version %q: error = %v, want a development-build error", version, err)
		}
	}
}

func TestParseVersion(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"0.1.0", "0.1.0"},
		{"v0.1.0", "0.1.0"},
		{"v0.1.0-3-gabc1234", "0.1.0"},
		{"v0.1.0-3-gabc1234-dirty", "0.1.0"},
		{"1.2.3 (commit abc, built today, linux/amd64)", "1.2.3"},
	}
	for _, test := range tests {
		got, err := parseVersion(test.raw)
		if err != nil {
			t.Errorf("parseVersion(%q): %v", test.raw, err)
			continue
		}
		if got.String() != test.want {
			t.Errorf("parseVersion(%q) = %s, want %s", test.raw, got, test.want)
		}
	}
}

func TestHumanSize(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{14 * 1024 * 1024, "14.0 MB"},
	}
	for _, test := range tests {
		if got := humanSize(test.in); got != test.want {
			t.Errorf("humanSize(%d) = %q, want %q", test.in, got, test.want)
		}
	}
}
