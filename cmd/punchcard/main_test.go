package main

import (
	"bytes"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
