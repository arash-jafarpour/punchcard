// Package telegram is a small client for the subset of the Telegram Bot API
// this tool needs: delivering the daily report as a document.
//
// Traffic goes direct unless TELEGRAM_PROXY is explicitly set. An unrelated
// HTTPS_PROXY in the environment must not silently reroute bot traffic, the
// same rule as the proxy-disabled GitLab transport. Errors returned by this
// package never carry the bot token or proxy credentials: *url.Error values
// are unwrapped and secrets are scrubbed before an error reaches a log.
package telegram

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultUserAgent    = "punchcard/1.0"
	defaultTimeout      = 30 * time.Second
	defaultMaxAttempts  = 3
	maxIdleConnsPerHost = 16
)

// Client talks to the Telegram Bot API for one bot.
type Client struct {
	base        *url.URL
	token       string
	httpClient  *http.Client
	userAgent   string
	maxAttempts int
	timeout     time.Duration

	// sleep is nil in production so retry backoff honours the request
	// context. Tests inject a no-op or a recorder.
	sleep func(time.Duration)

	// proxyRaw is the value passed to WithProxy, validated in New. An invalid
	// value fails construction instead of falling back to a direct connection.
	proxyRaw string
	// proxyPassword is kept solely so redactError can scrub it from errors.
	proxyPassword string
}

// Option customises a Client.
type Option func(*Client)

// WithHTTPClient replaces the HTTP client (tests). It takes precedence over
// WithProxy: when a client is injected, proxy transport construction is
// intentionally skipped and the injected client is used verbatim.
func WithHTTPClient(hc *http.Client) Option { return func(c *Client) { c.httpClient = hc } }

// WithProxy routes Bot API traffic through raw. Supported schemes are socks5,
// socks5h, http, and https. An empty value keeps traffic direct; any other
// value is validated by New, which fails fast rather than going direct.
func WithProxy(raw string) Option { return func(c *Client) { c.proxyRaw = raw } }

// WithSleep replaces the backoff sleeper (tests use a no-op).
func WithSleep(fn func(time.Duration)) Option { return func(c *Client) { c.sleep = fn } }

// WithMaxAttempts overrides the total number of attempts (the initial try plus
// retries). Negative values are ignored.
func WithMaxAttempts(n int) Option {
	return func(c *Client) {
		if n >= 0 {
			c.maxAttempts = n
		}
	}
}

// WithTimeout overrides the per-request timeout. Non-positive values are
// ignored.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		if d > 0 {
			c.timeout = d
		}
	}
}

// WithUserAgent overrides the User-Agent header.
func WithUserAgent(ua string) Option { return func(c *Client) { c.userAgent = ua } }

// New returns a client for the Bot API at baseURL, authenticated with token.
// The base URL is normalised by trimming whitespace and trailing slashes and
// dropping any query or fragment; baseURL is expected without a /bot… suffix.
// Options are applied in order, and an invalid proxy fails construction.
func New(baseURL, token string, opts ...Option) (*Client, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("telegram: token is empty")
	}
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, fmt.Errorf("telegram: invalid base URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("telegram: invalid base URL scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, errors.New("telegram: base URL has no host")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery = ""
	u.Fragment = ""

	c := &Client{
		base:        u,
		token:       token,
		userAgent:   defaultUserAgent,
		maxAttempts: defaultMaxAttempts,
		timeout:     defaultTimeout,
	}
	for _, opt := range opts {
		opt(c)
	}

	proxy, password, err := parseProxy(c.proxyRaw)
	if err != nil {
		return nil, err
	}
	c.proxyPassword = password

	// Only build a transport when the caller did not inject a client.
	if c.httpClient == nil {
		c.httpClient = newHTTPClient(proxy, c.timeout)
	}
	return c, nil
}

// parseProxy validates a WithProxy value without ever echoing it: the raw
// string can carry credentials, so parse failures return a fixed message.
func parseProxy(raw string) (*url.URL, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, "", nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, "", errors.New("telegram: invalid proxy URL")
	}
	switch u.Scheme {
	case "socks5", "socks5h", "http", "https":
	default:
		return nil, "", fmt.Errorf("telegram: unsupported proxy scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, "", errors.New("telegram: proxy has no host")
	}
	var password string
	if u.User != nil {
		password, _ = u.User.Password()
	}
	return u, password, nil
}

// newHTTPClient returns an HTTP client whose transport proxies only through
// proxy. Cloning the default transport keeps sane dial/TLS settings while the
// explicit nil Proxy drops the environment proxy function.
func newHTTPClient(proxy *url.URL, timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if proxy != nil {
		transport.Proxy = http.ProxyURL(proxy)
	}
	transport.MaxIdleConnsPerHost = maxIdleConnsPerHost
	return &http.Client{Transport: transport, Timeout: timeout}
}
