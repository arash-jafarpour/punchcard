// Package testutil provides fixture and fake-server helpers shared by the
// project's tests. It is test-only code and is never imported by the binary.
package testutil

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"gitlab.daal.ir/delijan/tools/gitlab-daily-report/internal/gitlab"
)

// Fixture reads a file from the repository's testdata directory. Tests run
// with the package directory as their working directory, so the repository
// root is always two levels up.
func Fixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

// JSON returns a handler that serves a fixture with a JSON content type.
func JSON(t *testing.T, name string) http.HandlerFunc {
	body := Fixture(t, name)
	return Body(string(body))
}

// Body returns a handler that serves a fixed body with status 200.
func Body(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}
}

// Status returns a handler with a fixed status code and body.
func Status(code int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = io.WriteString(w, body)
	}
}

// GitLabStub is a scriptable fake GitLab instance.
type GitLabStub struct {
	t      *testing.T
	server *httptest.Server

	mu     sync.Mutex
	routes map[string]http.HandlerFunc
	seen   []string
}

// NewGitLabStub starts a fake instance and closes it when the test ends.
func NewGitLabStub(t *testing.T) *GitLabStub {
	t.Helper()
	stub := &GitLabStub{t: t, routes: make(map[string]http.HandlerFunc)}
	stub.server = httptest.NewServer(http.HandlerFunc(stub.serve))
	t.Cleanup(stub.server.Close)
	return stub
}

// URL is the base URL of the fake instance.
func (s *GitLabStub) URL() string { return s.server.URL }

// Handle registers a handler for an exact request path.
func (s *GitLabStub) Handle(path string, handler http.HandlerFunc) {
	s.mu.Lock()
	s.routes[path] = handler
	s.mu.Unlock()
}

// JSON serves a fixture for an exact request path.
func (s *GitLabStub) JSON(path, fixture string) {
	s.Handle(path, JSON(s.t, fixture))
}

// Seen returns "METHOD path?query" for every request received.
func (s *GitLabStub) Seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.seen...)
}

// Client builds a GitLab client pointed at the stub.
func (s *GitLabStub) Client(token string, opts ...gitlab.Option) *gitlab.Client {
	s.t.Helper()
	client, err := gitlab.New(s.server.URL, token, opts...)
	if err != nil {
		s.t.Fatalf("create gitlab client: %v", err)
	}
	return client
}

func (s *GitLabStub) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.seen = append(s.seen, r.Method+" "+r.URL.RequestURI())
	handler := s.routes[r.URL.Path]
	s.mu.Unlock()

	if handler == nil {
		http.NotFound(w, r)
		return
	}
	handler(w, r)
}
