// Package config reads and validates the process configuration from the
// environment. Configuration is intentionally env-only: systemd uses
// EnvironmentFile and run.sh sources the same file for manual runs.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"gitlab.daal.ir/delijan/tools/gitlab-daily-report/internal/date"
)

// Defaults used when the corresponding environment variable is empty.
const (
	DefaultGitLabURL = "https://gitlab.daal.ir"
	DefaultReportDir = "./reports"
)

// Config is the fully resolved configuration. Location is derived from
// Timezone during Load so callers never load a timezone themselves.
type Config struct {
	GitLabURL        string
	GitLabToken      string
	GitLabUsername   string
	AuthorEmails     []string
	TelegramBotToken string
	TelegramChatID   string
	TelegramProxy    string
	Timezone         string
	Location         *time.Location
	ReportDir        string
	SendEmptyReport  bool
	LogLevel         slog.Level
}

// Load reads the configuration from the process environment.
func Load() (Config, error) { return LoadFrom(os.Getenv) }

// LoadFrom is Load with an injectable environment lookup, for tests.
func LoadFrom(getenv func(string) string) (Config, error) {
	cfg := Config{
		GitLabURL:        strings.TrimSpace(getenv("GITLAB_URL")),
		GitLabToken:      strings.TrimSpace(getenv("GITLAB_TOKEN")),
		GitLabUsername:   strings.TrimSpace(getenv("GITLAB_USERNAME")),
		AuthorEmails:     splitEmails(getenv("GITLAB_AUTHOR_EMAILS")),
		TelegramBotToken: strings.TrimSpace(getenv("TELEGRAM_BOT_TOKEN")),
		TelegramChatID:   strings.TrimSpace(getenv("TELEGRAM_CHAT_ID")),
		TelegramProxy:    strings.TrimSpace(getenv("TELEGRAM_PROXY")),
		Timezone:         strings.TrimSpace(getenv("REPORT_TIMEZONE")),
		ReportDir:        strings.TrimSpace(getenv("REPORT_DIR")),
	}

	if cfg.GitLabURL == "" {
		cfg.GitLabURL = DefaultGitLabURL
	}
	if cfg.Timezone == "" {
		cfg.Timezone = date.DefaultTimezone
	}
	if cfg.ReportDir == "" {
		cfg.ReportDir = DefaultReportDir
	}

	loc, err := date.ParseTimezone(cfg.Timezone)
	if err != nil {
		return Config{}, err
	}
	cfg.Location = loc

	cfg.SendEmptyReport = true
	if raw := strings.TrimSpace(getenv("SEND_EMPTY_REPORT")); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return Config{}, fmt.Errorf("invalid SEND_EMPTY_REPORT %q: want true or false", raw)
		}
		cfg.SendEmptyReport = v
	}

	level, err := parseLogLevel(getenv("LOG_LEVEL"))
	if err != nil {
		return Config{}, err
	}
	cfg.LogLevel = level

	return cfg, nil
}

// Validate checks the values that must be present before any network work.
// Telegram credentials are only required for a real send, never for --dry-run.
func (c Config) Validate(dryRun bool) error {
	if c.GitLabToken == "" {
		return errors.New("GITLAB_TOKEN is required")
	}
	if err := validateURL(c.GitLabURL); err != nil {
		return err
	}
	if c.ReportDir == "" {
		return errors.New("REPORT_DIR must not be empty")
	}
	if c.Location == nil {
		return errors.New("REPORT_TIMEZONE was not resolved")
	}
	if !dryRun {
		if c.TelegramBotToken == "" {
			return errors.New("TELEGRAM_BOT_TOKEN is required unless --dry-run is set")
		}
		if c.TelegramChatID == "" {
			return errors.New("TELEGRAM_CHAT_ID is required unless --dry-run is set")
		}
	}
	if c.TelegramProxy != "" {
		if err := ValidateProxy(c.TelegramProxy); err != nil {
			return err
		}
	}
	return nil
}

// ValidateProxy checks the shape of a TELEGRAM_PROXY value without dialling.
func ValidateProxy(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid TELEGRAM_PROXY: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "socks5", "socks5h", "http", "https":
	default:
		return fmt.Errorf("invalid TELEGRAM_PROXY scheme %q: want socks5, socks5h, http or https", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("invalid TELEGRAM_PROXY: missing host")
	}
	return nil
}

func validateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid GITLAB_URL: %w", err)
	}
	switch u.Scheme {
	case "http", "https":
	default:
		return fmt.Errorf("invalid GITLAB_URL %q: want an http or https URL", raw)
	}
	if u.Host == "" {
		return fmt.Errorf("invalid GITLAB_URL %q: missing host", raw)
	}
	return nil
}

func splitEmails(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.ToLower(strings.TrimSpace(part))
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func parseLogLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid LOG_LEVEL %q: want debug, info, warn or error", raw)
	}
}
