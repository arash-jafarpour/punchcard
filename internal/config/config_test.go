package config

import (
	"log/slog"
	"strings"
	"testing"
)

func envFrom(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := LoadFrom(envFrom(nil), LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.GitLabURL != DefaultGitLabURL {
		t.Errorf("GitLabURL = %q, want %q", cfg.GitLabURL, DefaultGitLabURL)
	}
	if cfg.Timezone != "Asia/Tehran" {
		t.Errorf("Timezone = %q, want Asia/Tehran", cfg.Timezone)
	}
	if cfg.ReportDir != DefaultReportDir {
		t.Errorf("ReportDir = %q, want %q", cfg.ReportDir, DefaultReportDir)
	}
	if !cfg.SendEmptyReport {
		t.Error("SendEmptyReport = false, want true by default")
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want info", cfg.LogLevel)
	}
	if cfg.Location == nil {
		t.Error("Location is nil")
	}
}

func TestValidateRequiresGitLabToken(t *testing.T) {
	cfg, err := LoadFrom(envFrom(nil), LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	err = cfg.Validate(true)
	if err == nil || !strings.Contains(err.Error(), "GITLAB_TOKEN") {
		t.Errorf("Validate(dryRun) = %v, want a GITLAB_TOKEN error", err)
	}
}

func TestTelegramRequiredOnlyForRealSend(t *testing.T) {
	cfg, err := LoadFrom(envFrom(map[string]string{"GITLAB_TOKEN": "token"}), LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(true); err != nil {
		t.Errorf("dry run should not need Telegram config: %v", err)
	}
	if err := cfg.Validate(false); err == nil || !strings.Contains(err.Error(), "TELEGRAM_BOT_TOKEN") {
		t.Errorf("real send error = %v, want a TELEGRAM_BOT_TOKEN error", err)
	}

	cfg.TelegramBotToken = "bot-token"
	if err := cfg.Validate(false); err == nil || !strings.Contains(err.Error(), "TELEGRAM_CHAT_ID") {
		t.Errorf("real send error = %v, want a TELEGRAM_CHAT_ID error", err)
	}

	cfg.TelegramChatID = "123456"
	if err := cfg.Validate(false); err != nil {
		t.Errorf("valid config rejected: %v", err)
	}
}

func TestValidateRejectsBadGitLabURL(t *testing.T) {
	cfg, err := LoadFrom(envFrom(map[string]string{
		"GITLAB_TOKEN": "token",
		"GITLAB_URL":   "ftp://gitlab.example.test",
	}), LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(true); err == nil {
		t.Error("expected an error for a non-HTTP GitLab URL")
	}
}

func TestLoadRejectsBadTimezone(t *testing.T) {
	_, err := LoadFrom(envFrom(map[string]string{"REPORT_TIMEZONE": "Mars/Olympus"}), LoadOptions{})
	if err == nil || !strings.Contains(err.Error(), "REPORT_TIMEZONE") {
		t.Errorf("error = %v, want a REPORT_TIMEZONE error", err)
	}
}

func TestLoadRejectsBadLogLevel(t *testing.T) {
	_, err := LoadFrom(envFrom(map[string]string{"LOG_LEVEL": "chatty"}), LoadOptions{})
	if err == nil || !strings.Contains(err.Error(), "LOG_LEVEL") {
		t.Errorf("error = %v, want a LOG_LEVEL error", err)
	}
}

func TestLoadRejectsBadSendEmptyReport(t *testing.T) {
	_, err := LoadFrom(envFrom(map[string]string{"SEND_EMPTY_REPORT": "sometimes"}), LoadOptions{})
	if err == nil || !strings.Contains(err.Error(), "SEND_EMPTY_REPORT") {
		t.Errorf("error = %v, want a SEND_EMPTY_REPORT error", err)
	}
	cfg, err := LoadFrom(envFrom(map[string]string{"SEND_EMPTY_REPORT": "false"}), LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SendEmptyReport {
		t.Error("SendEmptyReport = true, want false")
	}
}

func TestAuthorEmailsAreNormalised(t *testing.T) {
	cfg, err := LoadFrom(envFrom(map[string]string{
		"GITLAB_AUTHOR_EMAILS": " Arash@Daal.ir, ,second@example.test ",
	}), LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"arash@daal.ir", "second@example.test"}
	if len(cfg.AuthorEmails) != len(want) {
		t.Fatalf("emails = %v, want %v", cfg.AuthorEmails, want)
	}
	for i := range want {
		if cfg.AuthorEmails[i] != want[i] {
			t.Errorf("email[%d] = %q, want %q", i, cfg.AuthorEmails[i], want[i])
		}
	}
}

func TestValidateProxy(t *testing.T) {
	valid := []string{
		"socks5h://127.0.0.1:1089",
		"socks5://127.0.0.1:1089",
		"http://127.0.0.1:8080",
		"https://proxy.example.test:443",
		"socks5h://user:pass@127.0.0.1:1089",
	}
	for _, raw := range valid {
		if err := ValidateProxy(raw); err != nil {
			t.Errorf("ValidateProxy(%q) = %v, want nil", raw, err)
		}
	}

	invalid := []string{"ftp://127.0.0.1:21", "socks5h://", "127.0.0.1:1089"}
	for _, raw := range invalid {
		if err := ValidateProxy(raw); err == nil {
			t.Errorf("ValidateProxy(%q) = nil, want an error", raw)
		}
	}
}
