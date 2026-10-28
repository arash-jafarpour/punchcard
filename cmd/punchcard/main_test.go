package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arash-jafarpour/punchcard/internal/testutil"
)

func baseEnv(t *testing.T, overrides map[string]string) {
	t.Helper()
	values := map[string]string{
		"GITLAB_URL":           "",
		"GITLAB_TOKEN":         "test-token",
		"GITLAB_USERNAME":      "",
		"GITLAB_AUTHOR_EMAILS": "",
		"TELEGRAM_BOT_TOKEN":   "",
		"TELEGRAM_CHAT_ID":     "",
		"TELEGRAM_PROXY":       "",
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

func TestHelpExitsZero(t *testing.T) {
	baseEnv(t, nil)
	code, _, stderr := execute(t, "--help")
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stderr, "usage: punchcard") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestUnknownFlagExitsTwo(t *testing.T) {
	baseEnv(t, nil)
	code, _, stderr := execute(t, "--nope")
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "flag provided but not defined") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestUnexpectedArgumentExitsTwo(t *testing.T) {
	baseEnv(t, nil)
	code, _, stderr := execute(t, "surprise")
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "unexpected argument") {
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
	baseEnv(t, map[string]string{
		"GITLAB_URL":         stub.URL(),
		"TELEGRAM_BOT_TOKEN": "bot-token",
		"TELEGRAM_CHAT_ID":   "123456",
		"SEND_EMPTY_REPORT":  "false",
	})

	code, _, stderr := execute(t, "--date", "2026-10-27", "--report-dir", t.TempDir())
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr %q)", code, stderr)
	}
	if !strings.Contains(stderr, "telegram skipped") {
		t.Errorf("stderr = %q", stderr)
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
