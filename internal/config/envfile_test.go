package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseEnvFile(t *testing.T) {
	input := strings.Join([]string{
		"# a comment",
		"",
		"GITLAB_TOKEN=secret",
		"  REPORT_DIR = ./reports  ",
		"export TELEGRAM_CHAT_ID=123456",
		"export\tSEND_EMPTY_REPORT=false",
		`QUOTED="two words"`,
		"SINGLE='one word'",
		"EMPTY=",
		"WITH_EQUALS=a=b=c",
		"CRLF=windows\r",
	}, "\n")

	values, err := ParseEnvFile(strings.NewReader(input))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	want := map[string]string{
		"GITLAB_TOKEN":      "secret",
		"REPORT_DIR":        "./reports",
		"TELEGRAM_CHAT_ID":  "123456",
		"SEND_EMPTY_REPORT": "false",
		"QUOTED":            "two words",
		"SINGLE":            "one word",
		"EMPTY":             "",
		"WITH_EQUALS":       "a=b=c",
		"CRLF":              "windows",
	}
	for key, value := range want {
		if got := values[key]; got != value {
			t.Errorf("%s = %q, want %q", key, got, value)
		}
	}
	if _, ok := values["# a comment"]; ok {
		t.Error("a comment line became a value")
	}
	if len(values) != len(want) {
		t.Errorf("got %d values, want %d: %v", len(values), len(want), values)
	}
}

func TestParseEnvFileRejectsBadLines(t *testing.T) {
	bad := []string{
		"GITLAB_TOKEN",
		"=novalue",
		"BAD KEY=value",
		"1LEADING=value",
	}
	for _, input := range bad {
		if _, err := ParseEnvFile(strings.NewReader(input)); err == nil {
			t.Errorf("ParseEnvFile(%q) = nil error, want an error", input)
		}
	}
}

func TestParseEnvFileErrorHasTheLineNumber(t *testing.T) {
	input := "A=1\nB=2\nbroken\n"
	_, err := ParseEnvFile(strings.NewReader(input))
	if err == nil || !strings.Contains(err.Error(), "line 3") {
		t.Errorf("error = %v, want a line 3 error", err)
	}
}

func TestReadEnvFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env")
	if err := os.WriteFile(path, []byte("REPORT_DIR=/srv/reports\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	values, found, err := readEnvFile(path, false)
	if err != nil || !found {
		t.Fatalf("readEnvFile = %v, %v, want found", err, found)
	}
	if values["REPORT_DIR"] != "/srv/reports" {
		t.Errorf("REPORT_DIR = %q", values["REPORT_DIR"])
	}
}

func TestReadEnvFileMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent")

	if _, found, err := readEnvFile(path, false); err != nil || found {
		t.Errorf("optional file: err = %v, found = %v, want nil and false", err, found)
	}

	_, found, err := readEnvFile(path, true)
	if !errors.Is(err, ErrEnvFileNotFound) || found {
		t.Errorf("required file: err = %v, found = %v, want ErrEnvFileNotFound", err, found)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not name the path", err)
	}
}

func TestLoadReadsEnvFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env")
	content := "GITLAB_TOKEN=from-file\nREPORT_DIR=/srv/reports\nSEND_EMPTY_REPORT=false\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(envFrom(nil), LoadOptions{EnvFile: path})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.GitLabToken != "from-file" {
		t.Errorf("GitLabToken = %q, want from-file", cfg.GitLabToken)
	}
	if cfg.ReportDir != "/srv/reports" {
		t.Errorf("ReportDir = %q, want /srv/reports", cfg.ReportDir)
	}
	if cfg.SendEmptyReport {
		t.Error("SendEmptyReport = true, want false from the file")
	}
	if cfg.EnvFile != path {
		t.Errorf("EnvFile = %q, want %q", cfg.EnvFile, path)
	}
}

func TestLoadPrefersTheProcessEnvironment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env")
	content := "GITLAB_TOKEN=from-file\nREPORT_DIR=/srv/from-file\nLOG_LEVEL=debug\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	env := map[string]string{
		"GITLAB_TOKEN": "from-env",
		"REPORT_DIR":   "/srv/from-env",
	}
	cfg, err := LoadFrom(envFrom(env), LoadOptions{EnvFile: path})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.GitLabToken != "from-env" || cfg.ReportDir != "/srv/from-env" {
		t.Errorf("environment did not win: token %q, dir %q", cfg.GitLabToken, cfg.ReportDir)
	}
	if cfg.LogLevel.String() != "DEBUG" {
		t.Errorf("LogLevel = %v, want the file to fill a value that the environment omits", cfg.LogLevel)
	}
}

func TestLoadWithoutEnvFile(t *testing.T) {
	cfg, err := LoadFrom(envFrom(map[string]string{"GITLAB_TOKEN": "t"}), LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.EnvFile != "" {
		t.Errorf("EnvFile = %q, want empty", cfg.EnvFile)
	}
}

func TestDefaultEnvFileUsesTheConfigDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	got, err := DefaultEnvFile()
	if err != nil {
		t.Fatalf("DefaultEnvFile: %v", err)
	}
	want := filepath.Join(dir, "punchcard", "env")
	if got != want {
		t.Errorf("DefaultEnvFile = %q, want %q", got, want)
	}
}
