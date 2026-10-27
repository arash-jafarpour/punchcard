package gitlab_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"gitlab.daal.ir/delijan/tools/gitlab-daily-report/internal/gitlab"
	"gitlab.daal.ir/delijan/tools/gitlab-daily-report/internal/testutil"
)

const token = "secret-token"

func TestCurrentUserSendsAuthAndCaches(t *testing.T) {
	stub := testutil.NewGitLabStub(t)

	var (
		calls   int
		headers http.Header
	)
	stub.Handle("/api/v4/user", func(w http.ResponseWriter, r *http.Request) {
		calls++
		headers = r.Header.Clone()
		testutil.JSON(t, "user.json")(w, r)
	})

	client := stub.Client(token)
	first, err := client.CurrentUser(context.Background())
	if err != nil {
		t.Fatalf("CurrentUser: %v", err)
	}
	if first.Username != "arash.jafarpour" || first.ID != 7 {
		t.Errorf("user = %+v", first)
	}
	if got := headers.Get("PRIVATE-TOKEN"); got != token {
		t.Errorf("PRIVATE-TOKEN = %q, want %q", got, token)
	}
	if got := headers.Get("User-Agent"); !strings.HasPrefix(got, "gitlab-daily-report/") {
		t.Errorf("User-Agent = %q", got)
	}

	if _, err := client.CurrentUser(context.Background()); err != nil {
		t.Fatalf("second CurrentUser: %v", err)
	}
	if calls != 1 {
		t.Errorf("/user calls = %d, want 1 (cached)", calls)
	}
}

func TestProjectIsCached(t *testing.T) {
	stub := testutil.NewGitLabStub(t)
	calls := 0
	stub.Handle("/api/v4/projects/42", func(w http.ResponseWriter, r *http.Request) {
		calls++
		testutil.JSON(t, "project.json")(w, r)
	})

	client := stub.Client(token)
	for i := 0; i < 3; i++ {
		project, err := client.Project(context.Background(), 42)
		if err != nil {
			t.Fatalf("Project: %v", err)
		}
		if project.PathWithNamespace != "delijan/web/delijan-login-panel" {
			t.Errorf("project = %+v", project)
		}
	}
	if calls != 1 {
		t.Errorf("/projects/42 calls = %d, want 1 (cached)", calls)
	}
}

func TestEventsFollowsNextPage(t *testing.T) {
	stub := testutil.NewGitLabStub(t)
	stub.Handle("/api/v4/events", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("after") != "2026-10-26" || r.URL.Query().Get("before") != "2026-10-28" {
			t.Errorf("unexpected window: %s", r.URL.RawQuery)
		}
		if r.URL.Query().Get("scope") != "all" {
			t.Errorf("scope = %q, want all", r.URL.Query().Get("scope"))
		}
		switch r.URL.Query().Get("page") {
		case "1":
			w.Header().Set("X-Next-Page", "2")
			testutil.Body(`[{"id":1,"project_id":42,"created_at":"2026-10-27T10:00:00+03:30"},{"id":2,"project_id":42,"created_at":"2026-10-27T11:00:00+03:30"}]`)(w, r)
		case "2":
			testutil.Body(`[{"id":3,"project_id":88,"created_at":"2026-10-27T12:00:00+03:30"}]`)(w, r)
		default:
			t.Errorf("unexpected page %q", r.URL.Query().Get("page"))
			testutil.Body("[]")(w, r)
		}
	})

	client := stub.Client(token)
	events, err := client.Events(context.Background(), "2026-10-26", "2026-10-28")
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(events) != 3 {
		t.Errorf("events = %d, want 3", len(events))
	}
}

func TestEventsStopsOnShortPageWithoutHeader(t *testing.T) {
	stub := testutil.NewGitLabStub(t)
	requests := 0
	stub.Handle("/api/v4/events", func(w http.ResponseWriter, r *http.Request) {
		requests++
		testutil.Body(`[{"id":1,"project_id":42,"created_at":"2026-10-27T10:00:00+03:30"}]`)(w, r)
	})

	client := stub.Client(token)
	events, err := client.Events(context.Background(), "2026-10-26", "2026-10-28")
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("events = %d, want 1", len(events))
	}
	if requests != 1 {
		t.Errorf("requests = %d, want 1", requests)
	}
}

func TestRetryOnRateLimitHonoursRetryAfter(t *testing.T) {
	stub := testutil.NewGitLabStub(t)
	attempts := 0
	stub.Handle("/api/v4/user", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Retry-After", "2")
			testutil.Status(http.StatusTooManyRequests, `{"message":"slow down"}`)(w, r)
			return
		}
		testutil.JSON(t, "user.json")(w, r)
	})

	var delays []time.Duration
	client := stub.Client(token, gitlab.WithSleep(func(d time.Duration) { delays = append(delays, d) }))

	if _, err := client.CurrentUser(context.Background()); err != nil {
		t.Fatalf("CurrentUser: %v", err)
	}
	if attempts != 2 {
		t.Errorf("attempts = %d, want 2", attempts)
	}
	if len(delays) != 1 || delays[0] != 2*time.Second {
		t.Errorf("delays = %v, want [2s]", delays)
	}
}

func TestRetryOnServerErrorUsesBackoff(t *testing.T) {
	stub := testutil.NewGitLabStub(t)
	attempts := 0
	stub.Handle("/api/v4/user", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts <= 2 {
			testutil.Status(http.StatusBadGateway, `{"message":"bad gateway"}`)(w, r)
			return
		}
		testutil.JSON(t, "user.json")(w, r)
	})

	var delays []time.Duration
	client := stub.Client(token, gitlab.WithSleep(func(d time.Duration) { delays = append(delays, d) }))

	if _, err := client.CurrentUser(context.Background()); err != nil {
		t.Fatalf("CurrentUser: %v", err)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
	if len(delays) != 2 || delays[0] != time.Second || delays[1] != 2*time.Second {
		t.Errorf("delays = %v, want [1s 2s]", delays)
	}
}

func TestNoRetryOnNotFound(t *testing.T) {
	stub := testutil.NewGitLabStub(t)
	attempts := 0
	stub.Handle("/api/v4/projects/404", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		testutil.Status(http.StatusNotFound, `{"message":"404 Project Not Found"}`)(w, r)
	})

	client := stub.Client(token)
	_, err := client.Project(context.Background(), 404)
	if err == nil {
		t.Fatal("expected an error")
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 (no retry on 404)", attempts)
	}
	if !gitlab.IsNotFound(err) {
		t.Errorf("IsNotFound(%v) = false", err)
	}
	if !strings.Contains(err.Error(), "404 Project Not Found") {
		t.Errorf("error = %v, want GitLab's message", err)
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("error leaks the token: %v", err)
	}
}

func TestReviewerFallsBackToReviewerID(t *testing.T) {
	stub := testutil.NewGitLabStub(t)
	stub.Handle("/api/v4/merge_requests", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		switch {
		case query.Get("reviewer_username") != "":
			testutil.Status(http.StatusBadRequest, `{"message":"reviewer_username is not a valid parameter"}`)(w, r)
		case query.Get("reviewer_id") == "7":
			testutil.JSON(t, "review_merge_requests.json")(w, r)
		default:
			t.Errorf("unexpected query: %s", r.URL.RawQuery)
			testutil.Body("[]")(w, r)
		}
	})

	client := stub.Client(token)
	mrs, err := client.ReviewerMergeRequests(context.Background(), "arash.jafarpour", 7, "2026-10-27T00:00:00+03:30", "2026-10-28T00:00:00+03:30")
	if err != nil {
		t.Fatalf("ReviewerMergeRequests: %v", err)
	}
	if len(mrs) != 1 || mrs[0].IID != 41 {
		t.Fatalf("merge requests = %+v", mrs)
	}

	var sawUsername, sawID bool
	for _, request := range stub.Seen() {
		if strings.Contains(request, "reviewer_username=") {
			sawUsername = true
		}
		if strings.Contains(request, "reviewer_id=7") {
			sawID = true
		}
	}
	if !sawUsername || !sawID {
		t.Errorf("fallback requests missing: %v", stub.Seen())
	}
}

func TestReviewerErrorIsNotSwallowed(t *testing.T) {
	stub := testutil.NewGitLabStub(t)
	stub.Handle("/api/v4/merge_requests", testutil.Status(http.StatusForbidden, `{"message":"403 Forbidden"}`))

	client := stub.Client(token)
	_, err := client.ReviewerMergeRequests(context.Background(), "arash.jafarpour", 7, "a", "b")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !gitlab.IsStatus(err, http.StatusForbidden) {
		t.Errorf("error = %v, want a 403", err)
	}
}

func TestCompareAndCommitDecode(t *testing.T) {
	stub := testutil.NewGitLabStub(t)
	stub.JSON("/api/v4/projects/42/repository/compare", "compare.json")
	stub.JSON("/api/v4/projects/42/repository/commits/c20e74377000c7f5e49af014efa0612c394735ff", "commit.json")

	client := stub.Client(token)
	commits, err := client.Compare(context.Background(), 42, "aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111", "c20e74377000c7f5e49af014efa0612c394735ff")
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if len(commits) != 2 || commits[0].ID != "c20e74377000c7f5e49af014efa0612c394735ff" {
		t.Fatalf("commits = %+v", commits)
	}

	commit, err := client.Commit(context.Background(), 42, "c20e74377000c7f5e49af014efa0612c394735ff")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if commit.Stats == nil || commit.Stats.Additions != 69 || commit.Stats.Deletions != 40 {
		t.Errorf("stats = %+v", commit.Stats)
	}
}

func TestMergeRequestNotesDecode(t *testing.T) {
	stub := testutil.NewGitLabStub(t)
	stub.JSON("/api/v4/projects/42/merge_requests/41/notes", "notes.json")

	client := stub.Client(token)
	notes, err := client.MergeRequestNotes(context.Background(), 42, 41)
	if err != nil {
		t.Fatalf("MergeRequestNotes: %v", err)
	}
	if len(notes) != 5 {
		t.Fatalf("notes = %d, want 5", len(notes))
	}
	if notes[0].Author.Username == "" || notes[0].NoteableType != "MergeRequest" {
		t.Errorf("note[0] = %+v", notes[0])
	}
}

func TestAPIErrorIsReported(t *testing.T) {
	stub := testutil.NewGitLabStub(t)
	stub.Handle("/api/v4/user", testutil.Status(http.StatusUnauthorized, `{"message":"401 Unauthorized"}`))

	client := stub.Client(token)
	_, err := client.CurrentUser(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	var apiErr *gitlab.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("error = %v, want APIError 401", err)
	}
}
