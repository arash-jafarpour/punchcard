// Package gitlab is a small typed client for the subset of the GitLab REST
// API this tool needs. It deliberately uses its own transport with proxying
// disabled: the laptop may export HTTPS_PROXY for other work, but GitLab is
// reachable directly and must never be routed through the Telegram proxy.
package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	apiPrefix         = "/api/v4"
	perPage           = 100
	defaultUserAgent  = "gitlab-daily-report/1.0"
	defaultMaxRetries = 3
	requestTimeout    = 30 * time.Second
	maxResponseBytes  = 32 << 20
	maxPages          = 500
)

// APIError is a non-2xx GitLab response.
type APIError struct {
	StatusCode int
	Message    string
	Path       string
}

func (e *APIError) Error() string {
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}
	return fmt.Sprintf("gitlab: %s returned %d: %s", e.Path, e.StatusCode, msg)
}

// IsStatus reports whether err is an APIError with the given status code.
func IsStatus(err error, code int) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == code
	}
	return false
}

// IsNotFound reports whether err is a 404 from GitLab.
func IsNotFound(err error) bool { return IsStatus(err, http.StatusNotFound) }

// Client talks to one GitLab instance.
type Client struct {
	base       *url.URL
	token      string
	httpClient *http.Client
	userAgent  string
	maxRetries int
	// sleep is nil in production so backoff honours the request context.
	// Tests inject a no-op or a recorder.
	sleep func(time.Duration)

	mu       sync.Mutex
	user     *User
	projects map[int]Project
}

// Option customises a Client.
type Option func(*Client)

// WithHTTPClient replaces the HTTP client (tests).
func WithHTTPClient(hc *http.Client) Option { return func(c *Client) { c.httpClient = hc } }

// WithUserAgent overrides the User-Agent header.
func WithUserAgent(ua string) Option { return func(c *Client) { c.userAgent = ua } }

// WithMaxRetries overrides the retry budget for 429/5xx responses.
func WithMaxRetries(n int) Option {
	return func(c *Client) {
		if n >= 0 {
			c.maxRetries = n
		}
	}
}

// WithSleep replaces the backoff sleeper (tests use a no-op).
func WithSleep(fn func(time.Duration)) Option { return func(c *Client) { c.sleep = fn } }

// New returns a client for baseURL (either the instance root or a URL that
// already ends in /api/v4) authenticated with a personal access token.
func New(baseURL, token string, opts ...Option) (*Client, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("gitlab: token is empty")
	}
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, fmt.Errorf("gitlab: invalid base URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("gitlab: invalid base URL scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, errors.New("gitlab: base URL has no host")
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	if !strings.HasSuffix(u.Path, apiPrefix) {
		u.Path += apiPrefix
	}
	u.RawQuery = ""
	u.Fragment = ""

	c := &Client{
		base:       u,
		token:      token,
		httpClient: newHTTPClient(),
		userAgent:  defaultUserAgent,
		maxRetries: defaultMaxRetries,
		projects:   make(map[int]Project),
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.httpClient == nil {
		c.httpClient = newHTTPClient()
	}
	return c, nil
}

// newHTTPClient returns an HTTP client whose transport never proxies. Cloning
// the default transport keeps sane dial/TLS settings while dropping the
// environment proxy function.
func newHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.MaxIdleConnsPerHost = 16
	return &http.Client{Transport: transport, Timeout: requestTimeout}
}

// get performs a GET and decodes the JSON body into out (when non-nil).
func (c *Client) get(ctx context.Context, path string, query url.Values, out any) (http.Header, error) {
	u := *c.base
	u.Path = c.base.Path + path
	if query != nil {
		u.RawQuery = query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("gitlab: build request: %w", err)
	}
	return c.do(req, out)
}

// do executes req with authentication, retries and JSON decoding.
func (c *Client) do(req *http.Request, out any) (http.Header, error) {
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		r := req.Clone(req.Context())
		r.Header.Set("PRIVATE-TOKEN", c.token)
		r.Header.Set("User-Agent", c.userAgent)
		r.Header.Set("Accept", "application/json")

		resp, err := c.httpClient.Do(r)
		if err != nil {
			return nil, fmt.Errorf("gitlab: %s %s: %w", req.Method, req.URL.Path, err)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("gitlab: read %s: %w", req.URL.Path, readErr)
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if out != nil {
				if err := json.Unmarshal(body, out); err != nil {
					return nil, fmt.Errorf("gitlab: decode %s: %w", req.URL.Path, err)
				}
			}
			return resp.Header, nil
		}

		apiErr := &APIError{StatusCode: resp.StatusCode, Message: extractMessage(body), Path: req.URL.Path}
		retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		if !retryable || attempt == c.maxRetries {
			return nil, apiErr
		}
		delay := backoff(attempt)
		if resp.StatusCode == http.StatusTooManyRequests {
			if d, ok := retryAfter(resp.Header.Get("Retry-After")); ok {
				delay = d
			}
		}
		c.wait(req.Context(), delay)
		lastErr = apiErr
	}
	return nil, lastErr
}

func (c *Client) wait(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	if c.sleep != nil {
		c.sleep(d)
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func backoff(attempt int) time.Duration {
	if attempt > 3 {
		attempt = 3
	}
	return time.Second << attempt
}

func retryAfter(raw string) (time.Duration, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(raw); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second, true
	}
	if when, err := http.ParseTime(raw); err == nil {
		if d := time.Until(when); d > 0 {
			return d, true
		}
	}
	return 0, false
}

// extractMessage pulls a human-readable error out of a GitLab error body
// without ever echoing request headers.
func extractMessage(body []byte) string {
	var payload struct {
		Message json.RawMessage `json:"message"`
		Error   string          `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err == nil {
		if len(payload.Message) > 0 {
			var s string
			if json.Unmarshal(payload.Message, &s) == nil {
				return truncate(s, 300)
			}
			return truncate(string(payload.Message), 300)
		}
		if payload.Error != "" {
			return truncate(payload.Error, 300)
		}
	}
	return truncate(string(body), 300)
}

func truncate(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…"
}

// paginate follows X-Next-Page until the last page. When the header is
// missing it stops on a short page, with a hard page cap as a loop guard.
func paginate[T any](ctx context.Context, c *Client, path string, query url.Values) ([]T, error) {
	var out []T
	page := 1
	seen := make(map[int]bool)
	for pages := 0; pages < maxPages; pages++ {
		if seen[page] {
			break
		}
		seen[page] = true

		q := cloneValues(query)
		q.Set("per_page", strconv.Itoa(perPage))
		q.Set("page", strconv.Itoa(page))

		var items []T
		header, err := c.get(ctx, path, q, &items)
		if err != nil {
			return out, err
		}
		out = append(out, items...)

		next := strings.TrimSpace(header.Get("X-Next-Page"))
		if next == "" {
			if len(items) < perPage {
				break
			}
			page++
			continue
		}
		nextPage, convErr := strconv.Atoi(next)
		if convErr != nil || nextPage <= 0 || nextPage == page {
			break
		}
		page = nextPage
	}
	return out, nil
}

func cloneValues(in url.Values) url.Values {
	out := make(url.Values, len(in))
	for k, v := range in {
		out[k] = append([]string(nil), v...)
	}
	return out
}
