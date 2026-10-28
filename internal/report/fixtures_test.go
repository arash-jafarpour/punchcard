package report

import (
	"context"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/arash-jafarpour/punchcard/internal/date"
	"github.com/arash-jafarpour/punchcard/internal/gitlab"
	"github.com/arash-jafarpour/punchcard/internal/testutil"
)

const (
	testToken = "test-token"
	testDay   = "2026-10-27"
)

func fixtureStub(t *testing.T) *testutil.GitLabStub {
	t.Helper()
	stub := testutil.NewGitLabStub(t)

	stub.JSON("/api/v4/events", "events.json")
	stub.JSON("/api/v4/projects/42", "project.json")
	stub.JSON("/api/v4/projects/77", "project-driver-app.json")
	stub.JSON("/api/v4/projects/88", "project-d-ui.json")
	stub.JSON("/api/v4/projects/42/repository/compare", "compare.json")
	stub.JSON("/api/v4/projects/42/repository/commits/c20e74377000c7f5e49af014efa0612c394735ff", "commit.json")
	stub.JSON("/api/v4/projects/42/repository/commits/813d11afa266b68c29bec1e6f16ed40d0c9a2657", "commit-813.json")
	stub.JSON("/api/v4/projects/88/repository/commits", "commits.json")
	stub.JSON("/api/v4/projects/88/repository/commits/d0e5f00d11aa22bb33cc44dd55ee66ff77889900", "commit-d-ui.json")
	stub.JSON("/api/v4/projects/42/merge_requests/41/notes", "notes.json")
	stub.JSON("/api/v4/projects/42/merge_requests/42/notes", "notes-authored.json")

	stub.Handle("/api/v4/merge_requests", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("reviewer_username") != "" || query.Get("reviewer_id") != "" {
			testutil.JSON(t, "review_merge_requests.json")(w, r)
			return
		}
		testutil.JSON(t, "merge_requests.json")(w, r)
	})

	return stub
}

func collectFixture(t *testing.T, opts Options) *Report {
	t.Helper()
	stub := fixtureStub(t)
	client := stub.Client(testToken, gitlab.WithSleep(func(time.Duration) {}))

	loc, err := date.ParseTimezone("Asia/Tehran")
	if err != nil {
		t.Fatalf("load timezone: %v", err)
	}
	rng, err := date.ParseDay(testDay, loc)
	if err != nil {
		t.Fatalf("parse day: %v", err)
	}

	user := &gitlab.User{ID: 7, Username: "arash.jafarpour", Name: "Arash Jafarpour", Email: "arash@daal.ir"}
	collector := NewCollector(client, slog.New(slog.DiscardHandler))
	rep, err := collector.Collect(context.Background(), rng, user, opts)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	return rep
}

func fixtureReport(t *testing.T) *Report {
	t.Helper()
	return collectFixture(t, Options{})
}

func projectByPath(t *testing.T, rep *Report, path string) Project {
	t.Helper()
	for _, project := range rep.Projects {
		if project.PathWithNamespace == path {
			return project
		}
	}
	t.Fatalf("project %q not in report (have %d projects)", path, len(rep.Projects))
	return Project{}
}
