package report

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/arash-jafarpour/punchcard/internal/date"
)

var updateGolden = flag.Bool("update", false, "update golden files")

func TestRenderGolden(t *testing.T) {
	rep := fixtureReport(t)
	got := Render(rep)

	path := filepath.Join("testdata", "golden", "2026-10-27.md")
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create golden dir: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (regenerate with: go test ./internal/report -run TestRenderGolden -update): %v", err)
	}
	if string(want) != string(got) {
		t.Errorf("rendered report does not match %s\n--- want ---\n%s\n--- got ---\n%s", path, want, got)
	}
}

func TestRenderNoActivity(t *testing.T) {
	loc, err := date.ParseTimezone("Asia/Tehran")
	if err != nil {
		t.Fatal(err)
	}
	rep := &Report{
		Date:     "2026-10-26",
		Timezone: "Asia/Tehran",
		Location: loc,
		User:     UserInfo{Name: "Arash Jafarpour", Username: "arash.jafarpour"},
	}

	want := "# Daily report — 2026-10-26\n\n" +
		"**Arash Jafarpour** (@arash.jafarpour) · Asia/Tehran\n\n" +
		"## No activity\n\n" +
		"_No activity recorded for 2026-10-26._\n"

	if got := string(Render(rep)); got != want {
		t.Errorf("no-activity report mismatch\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}
}

func TestCleanInline(t *testing.T) {
	tests := []struct {
		name  string
		input string
		limit int
		want  string
	}{
		{name: "collapse", input: "a\n\nb\tc", limit: 100, want: "a b c"},
		{name: "brackets", input: "[wip] title", limit: 100, want: `\[wip\] title`},
		{name: "short", input: "small", limit: 100, want: "small"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := cleanInline(test.input, test.limit); got != test.want {
				t.Errorf("cleanInline(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}

	long := strings.Repeat("a", 130)
	got := cleanInline(long, 120)
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated value lacks an ellipsis: %q", got)
	}
	if n := utf8.RuneCountInString(got); n != 121 {
		t.Errorf("truncated length = %d runes, want 121", n)
	}
}

func TestWriteFile(t *testing.T) {
	rep := &Report{
		Date:     "2026-10-26",
		Timezone: "Asia/Tehran",
		Location: time.UTC,
		User:     UserInfo{Name: "Arash Jafarpour", Username: "arash.jafarpour"},
	}
	dir := t.TempDir()

	path, err := WriteFile(dir, rep)
	if err != nil {
		t.Fatalf("write file: %v", err)
	}
	if want := filepath.Join(dir, "2026-10-26.md"); path != want {
		t.Errorf("path = %q, want %q", path, want)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if !strings.Contains(string(data), "## No activity") {
		t.Errorf("file lacks no-activity section:\n%s", data)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %o, want 600", perm)
	}
}
