package report

import (
	"context"
	"strings"
	"testing"
	"time"

	"gitlab.daal.ir/delijan/tools/gitlab-daily-report/internal/date"
	"gitlab.daal.ir/delijan/tools/gitlab-daily-report/internal/gitlab"
	"gitlab.daal.ir/delijan/tools/gitlab-daily-report/internal/testutil"
)

func TestCollectFixtureDay(t *testing.T) {
	rep := fixtureReport(t)

	if rep.Date != testDay {
		t.Errorf("date = %q, want %q", rep.Date, testDay)
	}
	if rep.Timezone != "Asia/Tehran" {
		t.Errorf("timezone = %q, want Asia/Tehran", rep.Timezone)
	}
	if rep.User.Username != "arash.jafarpour" || rep.User.Name != "Arash Jafarpour" {
		t.Errorf("user = %+v", rep.User)
	}

	wantSummary := Summary{
		Projects:   3,
		Pushes:     4,
		Commits:    3,
		Additions:  86,
		Deletions:  45,
		MRsMerged:  1,
		MRsUpdated: 1,
		Issues:     2,
		Reviews:    3,
	}
	if rep.Summary != wantSummary {
		t.Errorf("summary = %+v, want %+v", rep.Summary, wantSummary)
	}

	wantOrder := []string{
		"delijan/web/d-ui",
		"delijan/web/delijan-driver-app",
		"delijan/web/delijan-login-panel",
	}
	if len(rep.Projects) != len(wantOrder) {
		t.Fatalf("projects = %d, want %d", len(rep.Projects), len(wantOrder))
	}
	for i, want := range wantOrder {
		if rep.Projects[i].PathWithNamespace != want {
			t.Errorf("project[%d] = %q, want %q", i, rep.Projects[i].PathWithNamespace, want)
		}
	}

	loginPanel := projectByPath(t, rep, "delijan/web/delijan-login-panel")
	if !loginPanel.Available || loginPanel.WebURL == "" {
		t.Errorf("login-panel metadata missing: %+v", loginPanel)
	}
	if len(loginPanel.Commits) != 2 {
		t.Fatalf("login-panel commits = %d, want 2 (dedupe failed)", len(loginPanel.Commits))
	}
	first, second := loginPanel.Commits[0], loginPanel.Commits[1]
	if first.SHA != "813d11afa266b68c29bec1e6f16ed40d0c9a2657" || second.SHA != "c20e74377000c7f5e49af014efa0612c394735ff" {
		t.Errorf("commit order = %s, %s", first.SHA, second.SHA)
	}
	if !second.HasStats || second.Additions != 69 || second.Deletions != 40 {
		t.Errorf("c20e stats = %+v", second)
	}
	if !second.AuthorIsUser {
		t.Errorf("c20e should be attributed to the current user")
	}
	pushedAt := mustTime(t, "2026-10-27T15:55:00+03:30")
	if !second.PushedAt.Equal(pushedAt) {
		t.Errorf("c20e pushed at %s, want %s", second.PushedAt, pushedAt)
	}
	if len(second.Refs) != 1 || second.Refs[0] != "feat/admin-identity-mapping-console" {
		t.Errorf("c20e refs = %v", second.Refs)
	}

	if len(loginPanel.MergeRequests) != 1 {
		t.Fatalf("login-panel MRs = %d, want 1", len(loginPanel.MergeRequests))
	}
	mr := loginPanel.MergeRequests[0]
	if mr.IID != 42 || mr.Action != ActionMerged || mr.CommentCount != 1 {
		t.Errorf("MR = %+v", mr)
	}

	if len(loginPanel.Reviews) != 1 {
		t.Fatalf("login-panel reviews = %d, want 1", len(loginPanel.Reviews))
	}
	review := loginPanel.Reviews[0]
	if review.IID != 41 || review.CommentCount != 2 || len(review.Excerpts) != 2 {
		t.Errorf("review = %+v", review)
	}

	if len(loginPanel.Issues) != 2 {
		t.Fatalf("login-panel issues = %d, want 2", len(loginPanel.Issues))
	}
	if loginPanel.Issues[0].IID != 17 || loginPanel.Issues[0].Action != "closed" {
		t.Errorf("issue[0] = %+v", loginPanel.Issues[0])
	}
	comment := loginPanel.Issues[1]
	if comment.IID != 18 || comment.Action != "commented" {
		t.Errorf("issue[1] = %+v", comment)
	}
	if comment.Note != "Added the filter to the endpoint. Next: tests." {
		t.Errorf("issue comment = %q", comment.Note)
	}

	dUI := projectByPath(t, rep, "delijan/web/d-ui")
	if len(dUI.Tags) != 1 || dUI.Tags[0] != "v0.4.0" {
		t.Errorf("d-ui tags = %v", dUI.Tags)
	}
	if len(dUI.Commits) != 1 || !dUI.Commits[0].HasStats || dUI.Commits[0].Additions != 5 {
		t.Errorf("d-ui commits = %+v", dUI.Commits)
	}
	if len(dUI.Branches) != 0 {
		t.Errorf("d-ui branches = %v, want none", dUI.Branches)
	}

	driverApp := projectByPath(t, rep, "delijan/web/delijan-driver-app")
	if driverApp.Pushes != 1 || driverApp.UnresolvedPushes != 1 || len(driverApp.Commits) != 0 {
		t.Errorf("driver-app = %+v", driverApp)
	}

	seen := make(map[string]int)
	for _, project := range rep.Projects {
		for _, commit := range project.Commits {
			seen[commit.SHA]++
		}
	}
	if len(seen) != 3 {
		t.Errorf("unique commits = %d, want 3: %v", len(seen), seen)
	}
	for sha, count := range seen {
		if count != 1 {
			t.Errorf("commit %s rendered %d times", sha, count)
		}
	}

	if !containsWarning(rep.Warnings, "project 77") {
		t.Errorf("expected a bulk-push warning for project 77, got %v", rep.Warnings)
	}
}

func TestCollectAuthorEmailFilter(t *testing.T) {
	rep := collectFixture(t, Options{AuthorEmails: []string{"someone-else@example.test"}})

	if rep.Summary.Commits != 0 {
		t.Errorf("commits = %d, want 0 after filtering", rep.Summary.Commits)
	}
	if rep.Summary.Lines() != 0 {
		t.Errorf("lines = %d, want 0 after filtering", rep.Summary.Lines())
	}
	if !containsWarning(rep.Warnings, "3 commits excluded by GITLAB_AUTHOR_EMAILS") {
		t.Errorf("missing exclusion warning: %v", rep.Warnings)
	}
}

func TestReviewNoteEnrichmentCap(t *testing.T) {
	rep := collectFixture(t, Options{MaxMRsWithNotes: 1})

	// MR !41 (the lowest IID) is enriched first; !42 is skipped by the cap.
	if rep.Summary.Reviews != 2 {
		t.Errorf("reviews = %d, want 2 notes from the first MR", rep.Summary.Reviews)
	}
	if !containsWarning(rep.Warnings, "capped at 1 merge requests") {
		t.Errorf("missing cap warning: %v", rep.Warnings)
	}
}

func TestCollectIgnoresOtherUsersEvents(t *testing.T) {
	stub := testutil.NewGitLabStub(t)
	stub.Handle("/api/v4/events", testutil.Body(`[
		{"id": 1, "project_id": 42, "author_username": "someone.else", "action_name": "pushed",
		 "created_at": "2026-10-27T10:00:00+03:30",
		 "push_data": {"commit_count": 1, "ref": "master", "ref_type": "branch",
		               "commit_to": "c20e74377000c7f5e49af014efa0612c394735ff"}}
	]`))
	stub.Handle("/api/v4/merge_requests", testutil.Body("[]"))

	client := stub.Client(testToken)
	loc, err := date.ParseTimezone("Asia/Tehran")
	if err != nil {
		t.Fatal(err)
	}
	rng, err := date.ParseDay(testDay, loc)
	if err != nil {
		t.Fatal(err)
	}

	rep, err := NewCollector(client, nil).Collect(context.Background(), rng,
		&gitlab.User{ID: 7, Username: "arash.jafarpour"}, Options{})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if rep.Summary.HasActivity() {
		t.Errorf("another user's event leaked into the report: %+v", rep.Summary)
	}
	for _, request := range stub.Seen() {
		if strings.HasPrefix(request, "GET /api/v4/projects/") {
			t.Errorf("foreign event triggered a project lookup: %s", request)
		}
	}
}

func TestCommitAccumPrefersPrecisePushTime(t *testing.T) {
	listing := mustTime(t, "2026-10-27T16:30:00+03:30")
	precise := mustTime(t, "2026-10-27T17:03:00+03:30")
	later := mustTime(t, "2026-10-27T18:00:00+03:30")

	// A listing approximation never overrides the push event that named the
	// commit, even when the precise observation arrives second.
	acc := &commitAccum{pushedAt: listing, precise: false}
	acc.merge(commitResult{pushedAt: precise, precise: true})
	if !acc.pushedAt.Equal(precise) || !acc.precise {
		t.Fatalf("merge = %s precise=%v, want %s precise=true", acc.pushedAt, acc.precise, precise)
	}

	acc.merge(commitResult{pushedAt: listing, precise: false})
	if !acc.pushedAt.Equal(precise) {
		t.Errorf("listing moved a precise time to %s", acc.pushedAt)
	}

	acc.merge(commitResult{pushedAt: later, precise: true})
	if !acc.pushedAt.Equal(precise) {
		t.Errorf("later precise time replaced the earlier one: %s", acc.pushedAt)
	}

	acc.merge(commitResult{pushedAt: listing, precise: true})
	if !acc.pushedAt.Equal(listing) {
		t.Errorf("earliest precise time not kept: %s", acc.pushedAt)
	}
}

func TestCollectEmptyDay(t *testing.T) {
	stub := testutil.NewGitLabStub(t)
	stub.Handle("/api/v4/events", testutil.Body("[]"))
	stub.Handle("/api/v4/merge_requests", testutil.Body("[]"))

	client := stub.Client(testToken)
	loc, err := date.ParseTimezone("Asia/Tehran")
	if err != nil {
		t.Fatal(err)
	}
	rng, err := date.ParseDay("2026-10-26", loc)
	if err != nil {
		t.Fatal(err)
	}

	collector := NewCollector(client, nil)
	rep, err := collector.Collect(context.Background(), rng, &gitlab.User{ID: 7, Username: "arash.jafarpour"}, Options{})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if rep.Summary.HasActivity() {
		t.Errorf("empty day has activity: %+v", rep.Summary)
	}
	if len(rep.Projects) != 0 {
		t.Errorf("projects = %d, want 0", len(rep.Projects))
	}
	if !strings.Contains(string(Render(rep)), "## No activity") {
		t.Errorf("empty report lacks the no-activity section:\n%s", Render(rep))
	}
}

func TestMRHeadlineActionPrecedence(t *testing.T) {
	loc, err := date.ParseTimezone("Asia/Tehran")
	if err != nil {
		t.Fatal(err)
	}
	rng, err := date.ParseDay(testDay, loc)
	if err != nil {
		t.Fatal(err)
	}
	day := mustTime(t, "2026-10-27T12:00:00+03:30")
	earlier := mustTime(t, "2026-10-26T09:00:00+03:30")

	merged := gitlab.MergeRequest{CreatedAt: earlier, MergedAt: timePtr(day)}
	if got := mrAction(merged, rng); got != ActionMerged {
		t.Errorf("merged action = %s, want merged", got)
	}

	closed := gitlab.MergeRequest{CreatedAt: earlier, ClosedAt: timePtr(day)}
	if got := mrAction(closed, rng); got != ActionClosed {
		t.Errorf("closed action = %s, want closed", got)
	}

	opened := gitlab.MergeRequest{CreatedAt: day}
	if got := mrAction(opened, rng); got != ActionOpened {
		t.Errorf("opened action = %s, want opened", got)
	}

	updated := gitlab.MergeRequest{CreatedAt: earlier}
	if got := mrAction(updated, rng); got != ActionUpdated {
		t.Errorf("updated action = %s, want updated", got)
	}
}

func TestIsCommitSHA(t *testing.T) {
	valid := []string{
		"c20e74377000c7f5e49af014efa0612c394735ff",
		"813d11afa266b68c29bec1e6f16ed40d0c9a2657",
		"ABC1234",
	}
	invalid := []string{
		"",
		"abc12",
		"0000000000000000000000000000000000000000",
		"refs/heads/master",
		"zzzzzzz",
	}
	for _, sha := range valid {
		if !isCommitSHA(sha) {
			t.Errorf("isCommitSHA(%q) = false, want true", sha)
		}
	}
	for _, sha := range invalid {
		if isCommitSHA(sha) {
			t.Errorf("isCommitSHA(%q) = true, want false", sha)
		}
	}
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse time %q: %v", value, err)
	}
	return parsed
}

func timePtr(t time.Time) *time.Time { return &t }

func containsWarning(warnings []string, needle string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, needle) {
			return true
		}
	}
	return false
}
