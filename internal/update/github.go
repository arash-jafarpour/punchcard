package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"
)

// DefaultAPIBase is the GitHub REST API root.
const DefaultAPIBase = "https://api.github.com"

type ghAsset struct {
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type ghRelease struct {
	TagName string    `json:"tag_name"`
	Name    string    `json:"name"`
	Body    string    `json:"body"`
	Assets  []ghAsset `json:"assets"`
}

// client talks to the GitHub release API for one repository.
type client struct {
	base  string
	slug  string
	http  *http.Client
	token string
}

func newClient(opts Options) *client {
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	base := opts.APIBase
	if base == "" {
		base = DefaultAPIBase
	}
	return &client{
		base:  base,
		slug:  opts.RepoSlug,
		http:  hc,
		token: os.Getenv("GITHUB_TOKEN"),
	}
}

// get sends one API request. GITHUB_TOKEN is optional: it raises the rate
// limit for public repositories and permits a private one.
func (c *client) get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "punchcard")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to reach %s: %w", c.base, err)
	}
	return resp, nil
}

// latestRelease returns the most recent published release.
func (c *client) latestRelease(ctx context.Context) (*ghRelease, error) {
	url := c.base + "/repos/" + c.slug + "/releases/latest"

	resp, err := c.get(ctx, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("no published releases for %s yet", c.slug)
		}
		if resp.StatusCode == http.StatusForbidden &&
			resp.Header.Get("X-RateLimit-Remaining") == "0" {
			return nil, fmt.Errorf(
				"GitHub API rate limit reached; retry later or set GITHUB_TOKEN",
			)
		}
		return nil, fmt.Errorf("GitHub API returned %s for %s", resp.Status, c.slug)
	}

	var release ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("failed to decode GitHub release: %w", err)
	}
	if release.TagName == "" {
		return nil, fmt.Errorf("no releases found for %s", c.slug)
	}
	return &release, nil
}

// download fetches one release asset and returns its body.
func (c *client) download(ctx context.Context, url string) (io.ReadCloser, error) {
	resp, err := c.get(ctx, url)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GitHub returned %s", resp.Status)
	}
	return resp.Body, nil
}

// assetForPlatform returns the asset that GoReleaser built for this platform:
// "punchcard_<os>_<arch>.tar.gz", or ".zip" on Windows. The archives
// name_template in .goreleaser.yaml keeps this naming.
func (r *ghRelease) assetForPlatform() (ghAsset, error) {
	prefix := fmt.Sprintf("%s_%s_%s.", binaryName, runtime.GOOS, runtime.GOARCH)
	for _, a := range r.Assets {
		if strings.HasPrefix(a.Name, prefix) {
			return a, nil
		}
	}
	return ghAsset{}, fmt.Errorf(
		"release %s has no asset for %s/%s", r.TagName, runtime.GOOS, runtime.GOARCH,
	)
}

// checksumFor resolves the split-checksum sidecar "<asset>.sha256" and returns
// the digest for assetName. The sidecar holds either bare hex or the
// "<hex>  <name>" form that GoReleaser writes.
func (c *client) checksumFor(ctx context.Context, r *ghRelease, assetName string) (string, error) {
	sidecar := assetName + ".sha256"

	var sum *ghAsset
	for i := range r.Assets {
		if r.Assets[i].Name == sidecar {
			sum = &r.Assets[i]
			break
		}
	}
	if sum == nil {
		return "", fmt.Errorf(
			"release %s has no %s sidecar (checksum.split must stay enabled in .goreleaser.yaml)",
			r.TagName, sidecar,
		)
	}

	body, err := c.download(ctx, sum.BrowserDownloadURL)
	if err != nil {
		return "", fmt.Errorf("failed to fetch %s: %w", sidecar, err)
	}
	defer body.Close()

	data, err := io.ReadAll(io.LimitReader(body, 64*1024))
	if err != nil {
		return "", fmt.Errorf("failed to read %s: %w", sidecar, err)
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		// A "<hex>  <name>" line must name our asset. A bare hex line
		// belongs to the sidecar asset by construction.
		if len(fields) >= 2 && !strings.HasSuffix(line, assetName) {
			continue
		}
		if validSHA256(fields[0]) {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("could not find a valid digest for %s in %s", assetName, sidecar)
}

// validSHA256 reports whether s is a 64-character hex digest in either case.
func validSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}
