// Package update replaces the running punchcard binary with the latest GitHub
// release. It checks the release archive against the GoReleaser split-checksum
// sidecar before it touches the current binary.
package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// binaryName is the file that each release archive carries.
const binaryName = "punchcard"

// semverRe extracts the base semantic version from a version string. It ignores
// a leading "v" and any git-describe suffix, e.g. "v0.1.0-2-gabc123-dirty".
var semverRe = regexp.MustCompile(`\d+\.\d+\.\d+`)

// Options configures one self-update run.
type Options struct {
	// CurrentVersion is the version string of the running build.
	CurrentVersion string
	// RepoSlug is the "owner/name" GitHub repository that holds the releases.
	RepoSlug string
	// CheckOnly reports a newer version without installing it.
	CheckOnly bool
	// Out receives the progress lines. Defaults to os.Stdout.
	Out io.Writer
	// Exe is the binary to replace. Defaults to the running executable.
	Exe string
	// HTTPClient fetches the release. Defaults to a client with a 30s timeout.
	HTTPClient *http.Client
	// APIBase overrides the GitHub API root. Tests use it.
	APIBase string
}

// Run checks for a newer release and, unless Opts.CheckOnly is set, installs
// it over the running binary.
func Run(ctx context.Context, opts Options) error {
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}

	current, err := parseVersion(opts.CurrentVersion)
	if err != nil {
		return err
	}

	c := newClient(opts)
	release, err := c.latestRelease(ctx)
	if err != nil {
		return err
	}

	latest, err := semver.NewVersion(release.TagName)
	if err != nil {
		return fmt.Errorf("could not parse release tag %q: %w", release.TagName, err)
	}
	if !latest.GreaterThan(current) {
		_, _ = fmt.Fprintf(out, "punchcard is already up to date (v%s)\n", current)
		return nil
	}
	if opts.CheckOnly {
		_, _ = fmt.Fprintf(out, "new version available: v%s (current v%s)\n", latest, current)
		return nil
	}

	exe := opts.Exe
	if exe == "" {
		if exe, err = os.Executable(); err != nil {
			return fmt.Errorf("could not locate the punchcard executable: %w", err)
		}
	}

	_, _ = fmt.Fprintf(out, "updating punchcard v%s -> v%s ...\n", current, latest)
	if err := c.install(ctx, release, exe, out); err != nil {
		return fmt.Errorf(
			"update failed: %w\n"+
				"if punchcard lives in a system directory, run with elevated permissions or reinstall",
			err,
		)
	}

	_, _ = fmt.Fprintf(out, "successfully updated punchcard to v%s\n", latest)
	if release.Body != "" {
		_, _ = fmt.Fprintf(out, "\n%s\n", release.Body)
	}
	return nil
}

// parseVersion returns the base semantic version of a build. A development
// build has no version to compare, thus it cannot update itself.
func parseVersion(raw string) (*semver.Version, error) {
	base := semverRe.FindString(raw)
	if base == "" {
		return nil, errors.New(
			"this is a development build; reinstall from a release to enable self-update",
		)
	}
	v, err := semver.NewVersion(base)
	if err != nil {
		return nil, fmt.Errorf("could not parse version %q: %w", raw, err)
	}
	return v, nil
}

// install downloads the archive for this platform, verifies its checksum,
// extracts the binary and replaces exe. The current binary stays in place
// until the new archive passes the checksum.
func (c *client) install(ctx context.Context, release *ghRelease, exe string, out io.Writer) error {
	asset, err := release.assetForPlatform()
	if err != nil {
		return err
	}
	want, err := c.checksumFor(ctx, release, asset.Name)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(out, "downloading %s (%s)\n", asset.Name, humanSize(asset.Size))
	body, err := c.download(ctx, asset.BrowserDownloadURL)
	if err != nil {
		return fmt.Errorf("failed to download %s: %w", asset.Name, err)
	}
	defer body.Close()

	sum := sha256.New()
	var archive bytes.Buffer
	if _, err := io.Copy(&archive, io.TeeReader(body, sum)); err != nil {
		return fmt.Errorf("failed to download %s: %w", asset.Name, err)
	}
	if got := hex.EncodeToString(sum.Sum(nil)); got != want {
		return fmt.Errorf(
			"sha256 mismatch for %s (got %s, want %s); possible tampering or partial download",
			asset.Name, got, want,
		)
	}
	_, _ = fmt.Fprintln(out, "checksum verified")

	tmp, err := os.CreateTemp(filepath.Dir(exe), ".punchcard-update-*")
	if err != nil {
		return fmt.Errorf("failed to create update staging file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}

	if err := extractBinary(&archive, asset.Name, tmp); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		cleanup()
		return fmt.Errorf("failed to set permissions on the new binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("failed to finalize the new binary: %w", err)
	}

	return replaceBinary(tmpName, exe)
}

// replaceBinary moves the staged binary in place. POSIX allows a rename over a
// running executable. Windows does not, thus the old binary moves aside first.
func replaceBinary(tmpPath, exe string) error {
	var oldPath string
	if runtime.GOOS == "windows" {
		oldPath = exe + ".old"
		if err := os.Rename(exe, oldPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("could not move the running executable out of the way: %w", err)
		}
	}
	if err := os.Rename(tmpPath, exe); err != nil {
		if oldPath != "" {
			_ = os.Rename(oldPath, exe) // restore the old binary
		}
		_ = os.Remove(tmpPath)
		return fmt.Errorf("could not replace the executable: %w", err)
	}
	if oldPath != "" {
		_ = os.Remove(oldPath)
	}
	return nil
}

// humanSize formats a byte count for the download line.
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// extractBinary writes the punchcard entry of the archive to dst. The format
// comes from the asset name, thus tests can use either archive on any OS.
func extractBinary(archive *bytes.Buffer, assetName string, dst *os.File) error {
	if strings.HasSuffix(assetName, ".zip") {
		return extractFromZip(archive, dst)
	}
	return extractFromTarGz(archive, dst)
}

func extractFromTarGz(archive *bytes.Buffer, dst *os.File) error {
	gz, err := gzip.NewReader(bytes.NewReader(archive.Bytes()))
	if err != nil {
		return fmt.Errorf("asset is not a valid tar.gz: %w", err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return errors.New("archive contains no punchcard binary")
		}
		if err != nil {
			return fmt.Errorf("asset is not a valid tar.gz: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg || filepath.Base(hdr.Name) != binaryName {
			continue
		}
		if _, err := io.Copy(dst, tr); err != nil {
			return fmt.Errorf("failed to extract the punchcard binary: %w", err)
		}
		return nil
	}
}

func extractFromZip(archive *bytes.Buffer, dst *os.File) error {
	zr, err := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
	if err != nil {
		return fmt.Errorf("asset is not a valid zip: %w", err)
	}
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() || filepath.Base(zf.Name) != binaryName {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return fmt.Errorf("failed to read the punchcard entry: %w", err)
		}
		_, copyErr := io.Copy(dst, rc)
		_ = rc.Close()
		if copyErr != nil {
			return fmt.Errorf("failed to extract the punchcard binary: %w", copyErr)
		}
		return nil
	}
	return errors.New("archive contains no punchcard binary")
}
