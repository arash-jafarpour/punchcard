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
	if cfg.TelegramAPIBase != DefaultTelegramAPIBase {
		t.Errorf("TelegramAPIBase = %q, want %q", cfg.TelegramAPIBase, DefaultTelegramAPIBase)
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
	err = cfg.Validate(true)
	if err == nil {
		t.Fatal("expected an error for a non-HTTP GitLab URL")
	}
	want := `invalid GITLAB_URL "ftp://gitlab.example.test": want an http or https URL`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

func TestValidateTelegramAPIBase(t *testing.T) {
	tests := []struct {
		name    string
		unset   bool
		raw     string
		want    string
		wantErr bool
	}{
		{name: "unset uses the default", unset: true, want: DefaultTelegramAPIBase},
		{name: "empty uses the default", raw: "", want: DefaultTelegramAPIBase},
		{name: "whitespace uses the default", raw: "  ", want: DefaultTelegramAPIBase},
		{name: "https host", raw: "https://bot.example.test", want: "https://bot.example.test"},
		{name: "loopback http", raw: "http://127.0.0.1:8080", want: "http://127.0.0.1:8080"},
		{name: "trailing slash removed", raw: "https://api.telegram.org/", want: "https://api.telegram.org"},
		{name: "multiple trailing slashes removed", raw: "https://api.telegram.org///", want: "https://api.telegram.org"},
		{name: "ftp scheme rejected", raw: "ftp://api.telegram.org", wantErr: true},
		{name: "missing host rejected", raw: "https://", wantErr: true},
		{name: "not a url rejected", raw: "not-a-url", wantErr: true},
		{name: "single slash rejected", raw: "/", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{"GITLAB_TOKEN": "token"}
			if !tc.unset {
				env["TELEGRAM_API_BASE"] = tc.raw
			}
			cfg, err := LoadFrom(envFrom(env), LoadOptions{})
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if !tc.wantErr && cfg.TelegramAPIBase != tc.want {
				t.Errorf("TelegramAPIBase = %q, want %q", cfg.TelegramAPIBase, tc.want)
			}
			err = cfg.Validate(true)
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "TELEGRAM_API_BASE") {
					t.Fatalf("Validate(true) = %v, want a TELEGRAM_API_BASE error", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate(true) = %v, want nil", err)
			}
		})
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
