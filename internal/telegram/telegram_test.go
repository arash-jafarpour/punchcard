package telegram

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestNewNormalisesBaseURL(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "no trailing slash", input: "https://api.telegram.org", want: "https://api.telegram.org"},
		{name: "trailing slash", input: "https://api.telegram.org/", want: "https://api.telegram.org"},
		{name: "surrounding whitespace", input: "  https://api.telegram.org/  ", want: "https://api.telegram.org"},
		{name: "query and fragment dropped", input: "https://api.telegram.org/?a=1#frag", want: "https://api.telegram.org"},
		{name: "sub path kept", input: "https://example.test/telegram/", want: "https://example.test/telegram"},
		{name: "http scheme", input: "http://127.0.0.1:8081", want: "http://127.0.0.1:8081"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, err := New(test.input, "token")
			if err != nil {
				t.Fatalf("New(%q): %v", test.input, err)
			}
			if got := client.base.String(); got != test.want {
				t.Errorf("New(%q) base = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestNewRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		token   string
	}{
		{name: "empty token", baseURL: "https://api.telegram.org", token: ""},
		{name: "whitespace token", baseURL: "https://api.telegram.org", token: "   \t\n"},
		{name: "ftp scheme", baseURL: "ftp://api.telegram.org", token: "token"},
		{name: "missing scheme", baseURL: "api.telegram.org", token: "token"},
		{name: "host-less", baseURL: "https://", token: "token"},
		{name: "unparseable", baseURL: ":// nope", token: "token"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, err := New(test.baseURL, test.token)
			if err == nil {
				t.Fatalf("New(%q, %q) = %+v, want an error", test.baseURL, test.token, client)
			}
			if !strings.HasPrefix(err.Error(), "telegram:") {
				t.Errorf("error = %q, want a telegram: prefix", err)
			}
		})
	}
}

func TestNewDefaults(t *testing.T) {
	client, err := New("https://api.telegram.org", "token")
	if err != nil {
		t.Fatal(err)
	}
	if client.userAgent != defaultUserAgent {
		t.Errorf("userAgent = %q, want %q", client.userAgent, defaultUserAgent)
	}
	if client.maxAttempts != defaultMaxAttempts {
		t.Errorf("maxAttempts = %d, want %d", client.maxAttempts, defaultMaxAttempts)
	}
	if client.timeout != defaultTimeout {
		t.Errorf("timeout = %v, want %v", client.timeout, defaultTimeout)
	}
	if client.httpClient.Timeout != defaultTimeout {
		t.Errorf("http client timeout = %v, want %v", client.httpClient.Timeout, defaultTimeout)
	}
	if client.proxyPassword != "" {
		t.Errorf("proxyPassword = %q, want empty", client.proxyPassword)
	}
	if client.sleep != nil {
		t.Error("sleep should be nil by default so backoff honours the context")
	}
}

func TestDefaultTransportIgnoresEnvironmentProxy(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("ALL_PROXY", "socks5://127.0.0.1:1")

	client, err := New("https://api.telegram.org", "token")
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := client.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", client.httpClient.Transport)
	}
	if transport.Proxy != nil {
		t.Error("Telegram transport must not inherit HTTPS_PROXY/HTTP_PROXY")
	}
	if transport.MaxIdleConnsPerHost != maxIdleConnsPerHost {
		t.Errorf("MaxIdleConnsPerHost = %d, want %d", transport.MaxIdleConnsPerHost, maxIdleConnsPerHost)
	}
}

func TestWithProxyUsesProxyURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "socks5", raw: "socks5://user:hunter2@127.0.0.1:1080"},
		{name: "socks5h", raw: "socks5h://user:hunter2@127.0.0.1:1080"},
		{name: "http", raw: "http://user:hunter2@127.0.0.1:8080"},
		{name: "https", raw: "https://user:hunter2@127.0.0.1:8443"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, err := New("https://api.telegram.org", "token", WithProxy(test.raw))
			if err != nil {
				t.Fatalf("New with proxy %q: %v", test.raw, err)
			}
			transport, ok := client.httpClient.Transport.(*http.Transport)
			if !ok {
				t.Fatalf("transport = %T, want *http.Transport", client.httpClient.Transport)
			}
			if transport.Proxy == nil {
				t.Fatal("transport.Proxy is nil, want http.ProxyURL")
			}
			req, err := http.NewRequest(http.MethodGet, "https://api.telegram.org/bot123/getMe", nil)
			if err != nil {
				t.Fatal(err)
			}
			got, err := transport.Proxy(req)
			if err != nil {
				t.Fatalf("proxy function: %v", err)
			}
			want, err := url.Parse(test.raw)
			if err != nil {
				t.Fatal(err)
			}
			if got.String() != want.String() {
				t.Errorf("proxy = %q, want %q", got, want)
			}
			if client.proxyPassword != "hunter2" {
				t.Errorf("proxyPassword = %q, want %q", client.proxyPassword, "hunter2")
			}
		})
	}
}

func TestWithProxyEmptyIsDirect(t *testing.T) {
	client, err := New("https://api.telegram.org", "token", WithProxy("  "))
	if err != nil {
		t.Fatal(err)
	}
	transport := client.httpClient.Transport.(*http.Transport)
	if transport.Proxy != nil {
		t.Error("an empty WithProxy value must keep traffic direct")
	}
}

func TestWithProxyRejectsInvalidValues(t *testing.T) {
	const token = "123:bot-secret-token"
	tests := []struct {
		name string
		raw  string
	}{
		{name: "socks4", raw: "socks4://user:hunter2@127.0.0.1:1080"},
		{name: "no scheme", raw: "127.0.0.1:1080"},
		{name: "host-less", raw: "http://"},
		{name: "unparseable", raw: "http://user:hunter2@:bad"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, err := New("https://api.telegram.org", token, WithProxy(test.raw))
			if err == nil {
				t.Fatalf("New with proxy %q = %+v, want an error", test.raw, client)
			}
			msg := err.Error()
			if !strings.HasPrefix(msg, "telegram:") {
				t.Errorf("error = %q, want a telegram: prefix", msg)
			}
			if strings.Contains(msg, token) {
				t.Errorf("error %q leaks the bot token", msg)
			}
			if strings.Contains(msg, "hunter2") {
				t.Errorf("error %q leaks the proxy password", msg)
			}
		})
	}
}

func TestWithTimeoutOverridesDefault(t *testing.T) {
	client, err := New("https://api.telegram.org", "token", WithTimeout(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if client.timeout != 5*time.Second {
		t.Errorf("timeout = %v, want 5s", client.timeout)
	}
	if client.httpClient.Timeout != 5*time.Second {
		t.Errorf("http client timeout = %v, want 5s", client.httpClient.Timeout)
	}

	for _, ignored := range []time.Duration{0, -time.Second} {
		client, err := New("https://api.telegram.org", "token", WithTimeout(ignored))
		if err != nil {
			t.Fatal(err)
		}
		if client.timeout != defaultTimeout {
			t.Errorf("WithTimeout(%v) changed timeout to %v, want %v", ignored, client.timeout, defaultTimeout)
		}
	}
}

func TestWithHTTPClientIsUsedVerbatim(t *testing.T) {
	injected := &http.Client{Timeout: 3 * time.Second}
	client, err := New("https://api.telegram.org", "token", WithHTTPClient(injected), WithProxy("http://127.0.0.1:1"))
	if err != nil {
		t.Fatal(err)
	}
	if client.httpClient != injected {
		t.Error("WithHTTPClient did not keep the injected client")
	}
	if client.httpClient.Timeout != 3*time.Second {
		t.Errorf("injected timeout = %v, want 3s", client.httpClient.Timeout)
	}
}

func TestWithHTTPClientStillValidatesProxy(t *testing.T) {
	injected := &http.Client{}
	if _, err := New("https://api.telegram.org", "token", WithHTTPClient(injected), WithProxy("socks4://127.0.0.1:1080")); err == nil {
		t.Error("an invalid proxy must fail even when a client is injected")
	}
}

func TestWithMaxAttempts(t *testing.T) {
	client, err := New("https://api.telegram.org", "token", WithMaxAttempts(5))
	if err != nil {
		t.Fatal(err)
	}
	if client.maxAttempts != 5 {
		t.Errorf("maxAttempts = %d, want 5", client.maxAttempts)
	}
	for _, ignored := range []int{-1, -100} {
		client, err := New("https://api.telegram.org", "token", WithMaxAttempts(ignored))
		if err != nil {
			t.Fatal(err)
		}
		if client.maxAttempts != defaultMaxAttempts {
			t.Errorf("WithMaxAttempts(%d) changed maxAttempts to %d, want %d", ignored, client.maxAttempts, defaultMaxAttempts)
		}
	}
}

func TestWithUserAgentAndSleep(t *testing.T) {
	var slept []time.Duration
	client, err := New("https://api.telegram.org", "token",
		WithUserAgent("custom-agent/2.0"),
		WithSleep(func(d time.Duration) { slept = append(slept, d) }),
	)
	if err != nil {
		t.Fatal(err)
	}
	if client.userAgent != "custom-agent/2.0" {
		t.Errorf("userAgent = %q, want %q", client.userAgent, "custom-agent/2.0")
	}
	if client.sleep == nil {
		t.Fatal("sleep was not installed")
	}
	client.sleep(2 * time.Second)
	if len(slept) != 1 || slept[0] != 2*time.Second {
		t.Errorf("slept = %v, want [2s]", slept)
	}
}

func TestNewAcceptsHTTPTestServerBase(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()

	client, err := New(server.URL+"/", "token", WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := client.base.String(), server.URL; got != want {
		t.Errorf("base = %q, want %q", got, want)
	}
}
