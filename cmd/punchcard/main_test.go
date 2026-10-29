package main

import (
	"bytes"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arash-jafarpour/punchcard/internal/date"
	"github.com/arash-jafarpour/punchcard/internal/report"
	"github.com/arash-jafarpour/punchcard/internal/testutil"
)

func baseEnv(t *testing.T, overrides map[string]string) {
	t.Helper()
	// Isolate the default env file. A test must not read the user's real one.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	values := map[string]string{
		"GITLAB_URL":           "",
		"GITLAB_TOKEN":         "test-token",
		"GITLAB_USERNAME":      "",
		"GITLAB_AUTHOR_EMAILS": "",
		"TELEGRAM_BOT_TOKEN":   "",
		"TELEGRAM_CHAT_ID":     "",
		"TELEGRAM_PROXY":       "",
		"TELEGRAM_API_BASE":    "",
		"REPORT_TIMEZONE":      "",
		"REPORT_DIR":           "",
		"SEND_EMPTY_REPORT":    "",
		"LOG_LEVEL":            "",
	}
	for key, value := range overrides {
		values[key] = value
	}
	for key, value := range values {
		t.Setenv(key, value)
	}
}

func execute(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestVersionFlag(t *testing.T) {
	baseEnv(t, nil)
	code, stdout, _ := execute(t, "--version")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout, "punchcard dev") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestShortVersionAndHelp(t *testing.T) {
	baseEnv(t, nil)

	code, stdout, _ := execute(t, "-V")
	if code != 0 || !strings.Contains(stdout, "punchcard dev") {
		t.Errorf("-V: code = %d, stdout = %q", code, stdout)
	}

	code, stdout, _ = execute(t, "-h")
	if code != 0 || !strings.Contains(stdout, "punchcard [flags]") {
		t.Errorf("-h: code = %d, stdout = %q", code, stdout)
	}
}

func TestShortDateAndVerbose(t *testing.T) {
	stub := newEmptyStub(t)
	baseEnv(t, map[string]string{"GITLAB_URL": stub.URL()})

	// The debug log line reports the env file, thus write one.
	envDir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "punchcard")
	if err := os.MkdirAll(envDir, 0o700); err != nil {
		t.Fatal(err)
	}
	content := []byte("REPORT_DIR=" + t.TempDir() + "\n")
	if err := os.WriteFile(filepath.Join(envDir, "env"), content, 0o600); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := execute(t, "-v", "-d", "2026-10-27", "--dry-run")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr %q)", code, stderr)
	}
	if !strings.Contains(stderr, "level=DEBUG") {
		t.Errorf("-v did not enable debug logging: %q", stderr)
	}
	if !strings.Contains(stderr, "date=2026-10-27") {
		t.Errorf("-d did not set the report date: %q", stderr)
	}
}

func TestHelpExitsZero(t *testing.T) {
	baseEnv(t, nil)
	code, stdout, _ := execute(t, "--help")
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout, "punchcard [flags]") {
		t.Errorf("stdout = %q, want the usage line", stdout)
	}
	if !strings.Contains(stdout, "--env-file") {
		t.Errorf("stdout = %q, want the env-file flag", stdout)
	}
}

func TestUnknownFlagExitsTwo(t *testing.T) {
	baseEnv(t, nil)
	code, _, stderr := execute(t, "--nope")
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "unknown flag") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestUnexpectedArgumentExitsTwo(t *testing.T) {
	baseEnv(t, nil)
	code, _, stderr := execute(t, "surprise")
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "unknown command") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestMissingTokenExitsOne(t *testing.T) {
	baseEnv(t, map[string]string{"GITLAB_TOKEN": ""})
	code, _, stderr := execute(t, "--dry-run")
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "GITLAB_TOKEN") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestRealRunNeedsTelegramConfig(t *testing.T) {
	baseEnv(t, nil)
	code, _, stderr := execute(t)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "TELEGRAM_BOT_TOKEN") {
		t.Errorf("stderr = %q", stderr)
	}
}

func newEmptyStub(t *testing.T) *testutil.GitLabStub {
	t.Helper()
	stub := testutil.NewGitLabStub(t)
	stub.Handle("/api/v4/user", testutil.JSON(t, "user.json"))
	stub.Handle("/api/v4/events", testutil.Body("[]"))
	stub.Handle("/api/v4/merge_requests", testutil.Body("[]"))
	return stub
}

func TestDryRunWritesReport(t *testing.T) {
	stub := newEmptyStub(t)
	dir := t.TempDir()
	envDir := filepath.Join(dir, "from-env")

	baseEnv(t, map[string]string{
		"GITLAB_URL": stub.URL(),
		"REPORT_DIR": envDir,
	})

	code, stdout, stderr := execute(t, "--date", "2026-10-27", "--dry-run", "--report-dir", dir)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr %q)", code, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty (logs go to stderr)", stdout)
	}
	if !strings.Contains(stderr, `msg="report generated"`) {
		t.Errorf("stderr lacks the report-generated log: %q", stderr)
	}
	if !strings.Contains(stderr, "date=2026-10-27") || !strings.Contains(stderr, "projects=0") {
		t.Errorf("log fields missing: %q", stderr)
	}
	if !strings.Contains(stderr, `msg="dry run: telegram skipped"`) {
		t.Errorf("stderr lacks the dry-run log: %q", stderr)
	}

	path := filepath.Join(dir, "2026-10-27.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if !strings.Contains(stderr, path) {
		t.Errorf("log lacks the absolute report path %q: %q", path, stderr)
	}
	if !strings.Contains(string(data), "## No activity") {
		t.Errorf("report lacks the no-activity section:\n%s", data)
	}
	if !strings.Contains(string(data), "_No activity recorded for 2026-10-27._") {
		t.Errorf("report lacks the dated no-activity line:\n%s", data)
	}

	if _, err := os.Stat(envDir); !os.IsNotExist(err) {
		t.Errorf("REPORT_DIR from the environment was used despite --report-dir: %v", err)
	}
}

func TestDryRunLogsAbsolutePathForRelativeReportDir(t *testing.T) {
	stub := newEmptyStub(t)
	workDir := t.TempDir()
	t.Chdir(workDir)
	baseEnv(t, map[string]string{"GITLAB_URL": stub.URL()})

	code, _, stderr := execute(t, "--date", "2026-10-27", "--dry-run", "--report-dir", "reports")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr %q)", code, stderr)
	}

	want, err := filepath.Abs(filepath.Join("reports", "2026-10-27.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, want) {
		t.Errorf("log lacks the absolute path %q: %q", want, stderr)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("report not written at %s: %v", want, err)
	}
}

func TestEmptyDaySkipsTelegramWhenDisabled(t *testing.T) {
	stub := newEmptyStub(t)
	tg := testutil.NewTelegramStub(t)
	baseEnv(t, map[string]string{
		"GITLAB_URL":         stub.URL(),
		"TELEGRAM_API_BASE":  tg.URL(),
		"TELEGRAM_BOT_TOKEN": "bot-token",
		"TELEGRAM_CHAT_ID":   "123456",
		"SEND_EMPTY_REPORT":  "false",
	})

	dir := t.TempDir()
	code, _, stderr := execute(t, "--date", "2026-10-27", "--report-dir", dir)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr %q)", code, stderr)
	}
	if !strings.Contains(stderr, "telegram skipped") {
		t.Errorf("stderr = %q", stderr)
	}
	if got := len(tg.Requests()); got != 0 {
		t.Errorf("telegram requests = %d, want 0", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "2026-10-27.md")); err != nil {
		t.Errorf("report not written: %v", err)
	}
}

// rangeFile returns the single Markdown report in dir with its start and end
// dates parsed from the file name. A one-day report has one date.
func rangeFile(t *testing.T, dir string) (path, start, end string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("report files = %v, want exactly one", matches)
	}
	stem := strings.TrimSuffix(filepath.Base(matches[0]), ".md")
	parts := strings.Split(stem, "_")
	if len(parts) > 2 {
		t.Fatalf("report file %q is neither YYYY-MM-DD nor YYYY-MM-DD_YYYY-MM-DD", stem)
	}
	for _, part := range parts {
		if _, err := time.Parse(date.Layout, part); err != nil {
			t.Fatalf("report file %q has a bad date %q: %v", stem, part, err)
		}
	}
	return matches[0], parts[0], parts[len(parts)-1]
}

func TestDaysFlagWritesRangeReport(t *testing.T) {
	stub := newEmptyStub(t)
	dir := t.TempDir()
	baseEnv(t, map[string]string{"GITLAB_URL": stub.URL()})

	code, stdout, stderr := execute(t, "--days", "3", "--dry-run", "--report-dir", dir)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr %q)", code, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty (logs go to stderr)", stdout)
	}
	if !strings.Contains(stderr, "days=3") {
		t.Errorf("log lacks the window size: %q", stderr)
	}
	if !strings.Contains(stderr, `msg="dry run: telegram skipped"`) {
		t.Errorf("stderr lacks the dry-run log: %q", stderr)
	}

	path, start, end := rangeFile(t, dir)
	if start == end {
		t.Fatalf("--days 3 wrote a single-day file %q", path)
	}
	startDay, err := time.Parse(date.Layout, start)
	if err != nil {
		t.Fatal(err)
	}
	endDay, err := time.Parse(date.Layout, end)
	if err != nil {
		t.Fatal(err)
	}
	if got := int(endDay.Sub(startDay).Hours() / 24); got != 2 {
		t.Errorf("window spans %d days after the start, want 2", got)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	wantTitle := fmt.Sprintf("# Activity report — %s → %s\n", start, end)
	if !strings.HasPrefix(string(data), wantTitle) {
		t.Errorf("report title = %q, want the window label %q",
			strings.SplitN(string(data), "\n", 2)[0], wantTitle)
	}
	if !strings.Contains(string(data), "## No activity") {
		t.Errorf("report lacks the no-activity section:\n%s", data)
	}
	if !strings.Contains(stderr, `window="`+start+" → "+end+`"`) {
		t.Errorf("log lacks the window label: %q", stderr)
	}
}

func TestDaysOneKeepsSingleDayFileName(t *testing.T) {
	stub := newEmptyStub(t)
	dir := t.TempDir()
	baseEnv(t, map[string]string{"GITLAB_URL": stub.URL()})

	code, _, stderr := execute(t, "--days", "1", "--dry-run", "--report-dir", dir)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr %q)", code, stderr)
	}

	path, start, end := rangeFile(t, dir)
	if start != end {
		t.Errorf("--days 1 wrote the range %q", filepath.Base(path))
	}
	if want := start + ".md"; filepath.Base(path) != want {
		t.Errorf("file = %q, want %q", filepath.Base(path), want)
	}

	// One day is still the ordinary daily document, not a window.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if want := "# Daily report — " + start + "\n"; !strings.HasPrefix(string(data), want) {
		t.Errorf("report does not start with %q:\n%s", want, data)
	}
}

func TestDaysFlagDeliversRangeDocument(t *testing.T) {
	gitlab := newEmptyStub(t)
	tg := testutil.NewTelegramStub(t)
	tg.Succeed(7777)

	dir := t.TempDir()
	baseEnv(t, map[string]string{
		"GITLAB_URL":         gitlab.URL(),
		"TELEGRAM_API_BASE":  tg.URL(),
		"TELEGRAM_BOT_TOKEN": "bot-token",
		"TELEGRAM_CHAT_ID":   "123456",
	})

	code, _, stderr := execute(t, "--days", "2", "--report-dir", dir)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr %q)", code, stderr)
	}
	if !strings.Contains(stderr, `msg="telegram delivered"`) {
		t.Errorf("stderr lacks the delivered log: %q", stderr)
	}

	path, start, end := rangeFile(t, dir)
	requests := tg.Requests()
	if len(requests) != 1 {
		t.Fatalf("telegram requests = %d, want 1", len(requests))
	}
	req := requests[0]
	if req.Err != nil {
		t.Fatalf("stub could not read the request: %v", req.Err)
	}
	if req.Method != "sendDocument" {
		t.Errorf("Bot API method = %q, want sendDocument", req.Method)
	}
	if want := filepath.Base(path); req.DocumentName != want {
		t.Errorf("document filename = %q, want %q", req.DocumentName, want)
	}
	wantCaption := fmt.Sprintf("GitLab report %s → %s · no activity", start, end)
	if req.Caption != wantCaption {
		t.Errorf("caption = %q, want %q", req.Caption, wantCaption)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if !bytes.Equal(req.Document, data) {
		t.Errorf("uploaded document differs from the file on disk (%d vs %d bytes)",
			len(req.Document), len(data))
	}
}

func TestDaysFlagValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "zero days",
			args: []string{"--days", "0"},
			want: "--days must be between 1 and 90",
		},
		{
			name: "too many days",
			args: []string{"--days", "91"},
			want: "--days must be between 1 and 90",
		},
		{
			name: "negative days",
			args: []string{"--days", "-1"},
			want: "--days must be between 1 and 90",
		},
		{
			name: "date and days",
			args: []string{"--date", "2026-10-27", "--days", "2"},
			want: "--date and --days cannot be combined",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseEnv(t, nil)
			args := append(append([]string(nil), tt.args...), "--dry-run")
			code, _, stderr := execute(t, args...)
			if code != 1 {
				t.Errorf("exit code = %d, want 1 (stderr %q)", code, stderr)
			}
			if !strings.Contains(stderr, tt.want) {
				t.Errorf("stderr = %q, want %q", stderr, tt.want)
			}
			if strings.Contains(stderr, "Usage:") {
				t.Errorf("a validation failure printed the usage text: %q", stderr)
			}
		})
	}
}

func TestWindowCaption(t *testing.T) {
	loc, err := date.ParseTimezone(date.DefaultTimezone)
	if err != nil {
		t.Fatal(err)
	}
	now, err := time.Parse(time.RFC3339, "2026-10-29T15:00:00+03:30")
	if err != nil {
		t.Fatal(err)
	}
	days := date.LastDays(now, loc, 3)

	// Projects are counted once across the window, not once per day.
	first := &report.Report{
		Date: "2026-10-27",
		Projects: []report.Project{
			{ID: 1, PathWithNamespace: "delijan/web/a"},
			{ID: 2, PathWithNamespace: "delijan/web/b"},
		},
		Summary: report.Summary{Projects: 2, Commits: 3, Additions: 5, Deletions: 1},
	}
	last := &report.Report{
		Date:     "2026-10-29",
		Projects: []report.Project{{ID: 1, PathWithNamespace: "delijan/web/a"}},
		Summary:  report.Summary{Projects: 1, Commits: 1, Additions: 1},
	}
	md := report.NewMultiDay(days, []*report.Report{first, last})

	want := "GitLab report 2026-10-27 → 2026-10-29 · 2 projects · 4 commits · +6/\u22121"
	if got := windowCaption(md); got != want {
		t.Errorf("windowCaption() = %q, want %q", got, want)
	}

	empty := report.NewMultiDay(days, nil)
	if got, want := windowCaption(empty), "GitLab report 2026-10-27 → 2026-10-29 · no activity"; got != want {
		t.Errorf("empty windowCaption() = %q, want %q", got, want)
	}
}

func TestCaption(t *testing.T) {
	tests := []struct {
		name string
		rep  report.Report
		want string
	}{
		{
			name: "full day",
			rep: report.Report{Date: "2026-10-27", Summary: report.Summary{
				Projects: 3, Commits: 12, Additions: 240, Deletions: 80,
			}},
			want: "GitLab daily report 2026-10-27 · 3 projects · 12 commits · +240/\u221280",
		},
		{
			name: "singular project and commit",
			rep: report.Report{Date: "2026-10-27", Summary: report.Summary{
				Projects: 1, Commits: 1, Additions: 5, Deletions: 0,
			}},
			want: "GitLab daily report 2026-10-27 · 1 project · 1 commit · +5/\u22120",
		},
		{
			name: "zero commits omitted",
			rep: report.Report{Date: "2026-10-27", Summary: report.Summary{
				Projects: 2, Commits: 0, Additions: 12, Deletions: 3,
			}},
			want: "GitLab daily report 2026-10-27 · 2 projects · +12/\u22123",
		},
		{
			name: "zero line changes omitted",
			rep: report.Report{Date: "2026-10-27", Summary: report.Summary{
				Projects: 1, Commits: 4,
			}},
			want: "GitLab daily report 2026-10-27 · 1 project · 4 commits",
		},
		{
			name: "empty day",
			rep:  report.Report{Date: "2026-10-27"},
			want: "GitLab daily report 2026-10-27 · no activity",
		},
		{
			name: "date passed through unchanged",
			rep:  report.Report{Date: "1999-12-31"},
			want: "GitLab daily report 1999-12-31 · no activity",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := caption(tt.rep); got != tt.want {
				t.Errorf("caption() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDeliversReportToTelegram(t *testing.T) {
	gitlab := newEmptyStub(t)
	tg := testutil.NewTelegramStub(t)
	tg.Succeed(4242)

	dir := t.TempDir()
	const (
		token  = "123456:test-bot-token"
		chatID = "-1001234567890"
	)
	baseEnv(t, map[string]string{
		"GITLAB_URL":         gitlab.URL(),
		"TELEGRAM_API_BASE":  tg.URL(),
		"TELEGRAM_BOT_TOKEN": token,
		"TELEGRAM_CHAT_ID":   chatID,
	})

	code, stdout, stderr := execute(t, "--date", "2026-10-27", "--report-dir", dir)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr %q)", code, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty (logs go to stderr)", stdout)
	}
	if !strings.Contains(stderr, `msg="telegram delivered"`) {
		t.Errorf("stderr lacks the delivered log: %q", stderr)
	}
	for _, field := range []string{"date=2026-10-27", "chat_id=" + chatID, "message_id=4242"} {
		if !strings.Contains(stderr, field) {
			t.Errorf("delivered log lacks %q: %q", field, stderr)
		}
	}

	path := filepath.Join(dir, "2026-10-27.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if !strings.Contains(stderr, path) {
		t.Errorf("delivered log lacks the absolute path %q: %q", path, stderr)
	}

	requests := tg.Requests()
	if len(requests) != 1 {
		t.Fatalf("telegram requests = %d, want 1", len(requests))
	}
	req := requests[0]
	if req.Err != nil {
		t.Fatalf("stub could not read the request: %v", req.Err)
	}
	if req.HTTPMethod != http.MethodPost {
		t.Errorf("HTTP method = %q, want POST", req.HTTPMethod)
	}
	if req.Method != "sendDocument" {
		t.Errorf("Bot API method = %q, want sendDocument", req.Method)
	}
	if req.Token != token {
		t.Errorf("token = %q, want the configured bot token", req.Token)
	}
	if req.ChatID != chatID {
		t.Errorf("chat_id = %q, want %q", req.ChatID, chatID)
	}
	if req.DocumentName != "2026-10-27.md" {
		t.Errorf("document filename = %q, want 2026-10-27.md", req.DocumentName)
	}
	mediaType, _, err := mime.ParseMediaType(req.DocumentType)
	if err != nil {
		t.Errorf("document Content-Type %q: %v", req.DocumentType, err)
	} else if mediaType != "text/markdown" {
		t.Errorf("document media type = %q, want text/markdown", mediaType)
	}
	if want := "GitLab daily report 2026-10-27 · no activity"; req.Caption != want {
		t.Errorf("caption = %q, want %q", req.Caption, want)
	}
	if !bytes.Equal(req.Document, data) {
		t.Errorf("uploaded document differs from the file on disk (%d vs %d bytes)",
			len(req.Document), len(data))
	}
}

func TestDeliveryFailureExitsOneAndKeepsReport(t *testing.T) {
	gitlab := newEmptyStub(t)
	tg := testutil.NewTelegramStub(t)
	// One queued answer per attempt: the client retries a 5xx three times.
	for range 3 {
		tg.ServerError()
	}

	// A dry run produces the bytes the failed send must leave untouched.
	dryDir := t.TempDir()
	const (
		token  = "987654:secret-bot-token"
		chatID = "123456"
	)
	baseEnv(t, map[string]string{
		"GITLAB_URL":         gitlab.URL(),
		"TELEGRAM_API_BASE":  tg.URL(),
		"TELEGRAM_BOT_TOKEN": token,
		"TELEGRAM_CHAT_ID":   chatID,
	})
	dryCode, _, dryStderr := execute(t, "--date", "2026-10-27", "--dry-run", "--report-dir", dryDir)
	if dryCode != 0 {
		t.Fatalf("dry run: exit code = %d, want 0 (stderr %q)", dryCode, dryStderr)
	}
	want, err := os.ReadFile(filepath.Join(dryDir, "2026-10-27.md"))
	if err != nil {
		t.Fatalf("read dry-run report: %v", err)
	}

	dir := t.TempDir()
	code, _, stderr := execute(t, "--date", "2026-10-27", "--report-dir", dir)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (stderr %q)", code, stderr)
	}
	if !strings.Contains(stderr, `msg="telegram delivery failed"`) {
		t.Errorf("stderr lacks the delivery-failed log: %q", stderr)
	}
	if strings.Contains(stderr, token) || strings.Contains(stderr, "secret-bot-token") {
		t.Errorf("stderr leaks the bot token: %q", stderr)
	}
	if got := len(tg.Requests()); got != 3 {
		t.Errorf("telegram requests = %d, want 3 attempts", got)
	}

	path := filepath.Join(dir, "2026-10-27.md")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the failed send lost the report: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("the report changed during the failed send (%d vs %d bytes)", len(got), len(want))
	}
}

func TestDryRunMakesNoTelegramRequest(t *testing.T) {
	gitlab := newEmptyStub(t)
	tg := testutil.NewTelegramStub(t)

	dir := t.TempDir()
	baseEnv(t, map[string]string{
		"GITLAB_URL":         gitlab.URL(),
		"TELEGRAM_API_BASE":  tg.URL(),
		"TELEGRAM_BOT_TOKEN": "bot-token",
		"TELEGRAM_CHAT_ID":   "123456",
	})

	code, _, stderr := execute(t, "--date", "2026-10-27", "--dry-run", "--report-dir", dir)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr %q)", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "2026-10-27.md")); err != nil {
		t.Errorf("report not written: %v", err)
	}
	if got := len(tg.Requests()); got != 0 {
		t.Errorf("telegram requests = %d, want 0", got)
	}
}

func TestInvalidDateExitsOne(t *testing.T) {
	baseEnv(t, nil)
	code, _, stderr := execute(t, "--date", "2026-02-30", "--dry-run")
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "invalid date") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestMissingExplicitEnvFileExitsTwo(t *testing.T) {
	baseEnv(t, nil)
	path := filepath.Join(t.TempDir(), "absent")

	code, _, stderr := execute(t, "--env-file", path, "--dry-run")
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "does not exist") || !strings.Contains(stderr, path) {
		t.Errorf("stderr = %q, want a missing-file error that names the path", stderr)
	}
}

func TestDefaultEnvFileSuppliesConfig(t *testing.T) {
	stub := newEmptyStub(t)
	baseEnv(t, map[string]string{"GITLAB_URL": stub.URL()})

	reportDir := t.TempDir()
	envDir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "punchcard")
	if err := os.MkdirAll(envDir, 0o700); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(envDir, "env")
	if err := os.WriteFile(envPath, []byte("REPORT_DIR="+reportDir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := execute(t, "--date", "2026-10-27", "--dry-run")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr %q)", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(reportDir, "2026-10-27.md")); err != nil {
		t.Errorf("the default env file did not set REPORT_DIR: %v", err)
	}
}

func TestEnvFileWiderThan0600Warns(t *testing.T) {
	stub := newEmptyStub(t)
	baseEnv(t, map[string]string{"GITLAB_URL": stub.URL()})

	envPath := filepath.Join(t.TempDir(), "env")
	if err := os.WriteFile(envPath, []byte("REPORT_DIR="+t.TempDir()+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := execute(t, "--env-file", envPath, "--date", "2026-10-27", "--dry-run")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr %q)", code, stderr)
	}
	if !strings.Contains(stderr, "readable by other users") {
		t.Errorf("stderr = %q, want a mode warning", stderr)
	}
}

func TestUpdateNeedsNoGitLabConfig(t *testing.T) {
	// A test build reports "dev", thus update stops before any network call.
	// An empty GITLAB_TOKEN shows that the command skips the report config.
	baseEnv(t, map[string]string{"GITLAB_TOKEN": ""})

	code, _, stderr := execute(t, "update", "--check")
	if code != 1 {
		t.Errorf("exit code = %d, want 1 (stderr %q)", code, stderr)
	}
	if !strings.Contains(stderr, "development build") {
		t.Errorf("stderr = %q, want the development-build error", stderr)
	}
	if strings.Contains(stderr, "Usage:") {
		t.Errorf("a failed run printed the usage text: %q", stderr)
	}
}
