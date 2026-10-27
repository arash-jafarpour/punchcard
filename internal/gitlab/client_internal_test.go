package gitlab

import (
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestNewNormalisesAPIBase(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "https://gitlab.daal.ir", want: "https://gitlab.daal.ir/api/v4"},
		{input: "https://gitlab.daal.ir/", want: "https://gitlab.daal.ir/api/v4"},
		{input: "https://gitlab.daal.ir/api/v4", want: "https://gitlab.daal.ir/api/v4"},
		{input: "https://example.test/gitlab", want: "https://example.test/gitlab/api/v4"},
	}
	for _, test := range tests {
		client, err := New(test.input, "token")
		if err != nil {
			t.Fatalf("New(%q): %v", test.input, err)
		}
		if got := client.base.String(); got != test.want {
			t.Errorf("New(%q) base = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestNewRejectsInvalidInput(t *testing.T) {
	if _, err := New("https://gitlab.example.test", ""); err == nil {
		t.Error("expected an error for an empty token")
	}
	if _, err := New("ftp://gitlab.example.test", "token"); err == nil {
		t.Error("expected an error for a non-HTTP scheme")
	}
	if _, err := New("not a url", "token"); err == nil {
		t.Error("expected an error for an unparseable URL")
	}
}

func TestDefaultTransportDoesNotProxy(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")

	client, err := New("https://gitlab.example.test", "token")
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := client.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", client.httpClient.Transport)
	}
	if transport.Proxy != nil {
		t.Error("GitLab transport must not use a proxy function")
	}
}

func TestExtractMessage(t *testing.T) {
	tests := []struct {
		body string
		want string
	}{
		{body: `{"message":"401 Unauthorized"}`, want: "401 Unauthorized"},
		{body: `{"error":"boom"}`, want: "boom"},
		{body: `{"message":["one","two"]}`, want: `["one","two"]`},
		{body: "not json", want: "not json"},
	}
	for _, test := range tests {
		if got := extractMessage([]byte(test.body)); got != test.want {
			t.Errorf("extractMessage(%q) = %q, want %q", test.body, got, test.want)
		}
	}
}

func TestExtractMessageTruncates(t *testing.T) {
	got := extractMessage([]byte(strings.Repeat("x", 500)))
	if n := utf8.RuneCountInString(got); n > 301 {
		t.Errorf("message length = %d runes, want <= 301", n)
	}
}
