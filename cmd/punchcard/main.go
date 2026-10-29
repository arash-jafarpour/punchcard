// Command punchcard generates the GitLab daily report and sends the Markdown
// file to Telegram as a document with a short caption.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/arash-jafarpour/punchcard/internal/config"
	"github.com/arash-jafarpour/punchcard/internal/date"
	"github.com/arash-jafarpour/punchcard/internal/gitlab"
	"github.com/arash-jafarpour/punchcard/internal/report"
	"github.com/arash-jafarpour/punchcard/internal/telegram"
)

// reportTimeout bounds the whole run; systemd's TimeoutStartSec is 300s.
const reportTimeout = 5 * time.Minute

// Build metadata, injected at build time via -ldflags. See the Makefile.
// The names avoid a clash with the imported date package.
var (
	buildVersion = "dev"
	buildCommit  = "none"
	buildDate    = "unknown"
)

// errRunFailed reports a failed run. The failure is already in the log, thus
// the command does not print the message again.
var errRunFailed = errors.New("run failed")

// options holds the command-line flags.
type options struct {
	date      string
	days      int
	envFile   string
	dryRun    bool
	reportDir string
	verbose   bool
}

// Window bounds for --days. The cap keeps one run's API usage, report size and
// upload predictable.
const (
	minDays = 1
	maxDays = 90
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// versionString is the text of --version.
func versionString() string {
	return fmt.Sprintf("%s (commit %s, built %s, %s/%s)",
		buildVersion, buildCommit, buildDate, runtime.GOOS, runtime.GOARCH)
}

// run runs the command and returns the exit code:
// 0 for success, 1 for a failed run, 2 for a usage problem.
func run(args []string, stdout, stderr io.Writer) int {
	started := false
	cmd := newRootCommand(&started)
	cmd.SetArgs(args)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)

	err := cmd.Execute()
	if err == nil {
		return 0
	}
	if errors.Is(err, errRunFailed) {
		return 1
	}

	_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)
	if !started {
		// The command line did not parse. Show the usage text.
		_, _ = fmt.Fprintln(stderr)
		_, _ = fmt.Fprint(stderr, cmd.UsageString())
		return 2
	}
	if errors.Is(err, config.ErrEnvFileNotFound) {
		return 2
	}
	return 1
}

// newRootCommand builds the command tree. started becomes true when the flags
// parse and the run starts, thus the caller can tell a usage error from a
// failed run.
func newRootCommand(started *bool) *cobra.Command {
	var opts options

	cmd := &cobra.Command{
		Use:   "punchcard",
		Short: "Generate the GitLab daily report",
		Long: "Generate a Markdown report of one user's GitLab activity for one day\n" +
			"or for a window of recent days.\n" +
			"The Markdown file is sent to TELEGRAM_CHAT_ID as a document with a short\n" +
			"caption unless --dry-run is set.\n\n" +
			"Precedence: flags, then the process environment, then the env file.\n" +
			"The env file holds KEY=VALUE lines and defaults to\n" +
			"$XDG_CONFIG_HOME/punchcard/env.",
		Example: "  punchcard --date 2026-10-27 --dry-run\n" +
			"  punchcard --days 7 --dry-run\n" +
			"  punchcard --env-file ./env --verbose",
		Version:       versionString(),
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		// Run before any command body. run() reads the flag to tell a usage
		// error (exit 2) from a failed run (exit 1).
		PersistentPreRunE: func(*cobra.Command, []string) error {
			*started = true
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return prepare(cmd, opts)
		},
	}
	cmd.SetVersionTemplate("{{.Name}} {{.Version}}\n")
	cmd.AddCommand(newUpdateCommand())

	flags := cmd.Flags()
	flags.StringVarP(&opts.date, "date", "d", "", "report date as YYYY-MM-DD (default: yesterday in the report timezone)")
	flags.IntVar(&opts.days, "days", 0, "report the last N days ending today (1-90); cannot be combined with --date")
	flags.StringVar(&opts.envFile, "env-file", "", "read KEY=VALUE lines from this file (default: $XDG_CONFIG_HOME/punchcard/env)")
	flags.BoolVar(&opts.dryRun, "dry-run", false, "write the Markdown file but do not contact Telegram")
	flags.StringVar(&opts.reportDir, "report-dir", "", "output directory (default: ./reports or REPORT_DIR)")
	flags.BoolVarP(&opts.verbose, "verbose", "v", false, "enable debug logging")
	// --version takes -V: cobra would use -v, but that belongs to --verbose.
	// A flag that we define here stops cobra from adding its own.
	flags.BoolP("version", "V", false, "print the version and exit")

	return cmd
}

// prepare loads the configuration, resolves the report day and runs the
// report. It returns an error that run maps to an exit code.
func prepare(cmd *cobra.Command, opts options) error {
	cfg, err := config.Load(loadOptions(opts.envFile))
	if err != nil {
		return err
	}
	if opts.reportDir != "" {
		cfg.ReportDir = opts.reportDir
	}
	if opts.verbose {
		cfg.LogLevel = slog.LevelDebug
	}
	if err := cfg.Validate(opts.dryRun); err != nil {
		return err
	}

	days, err := resolveDays(cmd, opts, cfg.Location)
	if err != nil {
		return err
	}

	stderr := cmd.ErrOrStderr()
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	if cfg.EnvFile != "" {
		logger.Debug("env file loaded", "path", cfg.EnvFile)
		warnEnvFileMode(logger, cfg.EnvFile)
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, reportTimeout)
	defer cancel()

	if code := runReport(ctx, cfg, days, opts.dryRun, logger); code != 0 {
		return errRunFailed
	}
	return nil
}

// resolveDays turns the flags into the ordered day window of the run. A bare
// run reports yesterday, --date reports one named day, and --days reports the
// last N days ending today. The window is the single definition that the
// collector, the file name and the caption share.
func resolveDays(cmd *cobra.Command, opts options, loc *time.Location) ([]date.Range, error) {
	daysSet := cmd.Flags().Changed("days")
	switch {
	case opts.date != "" && daysSet:
		return nil, errors.New("--date and --days cannot be combined")
	case daysSet && (opts.days < minDays || opts.days > maxDays):
		return nil, fmt.Errorf("--days must be between %d and %d, got %d",
			minDays, maxDays, opts.days)
	case daysSet:
		return date.LastDays(time.Now(), loc, opts.days), nil
	case opts.date != "":
		rng, err := date.ParseDay(opts.date, loc)
		if err != nil {
			return nil, err
		}
		return []date.Range{rng}, nil
	default:
		return []date.Range{date.Yesterday(time.Now(), loc)}, nil
	}
}

// generated is the artifact of one run: the file on disk plus the numbers the
// log and the Telegram caption report.
type generated struct {
	stem    string
	label   string
	path    string
	summary report.Summary
	caption string
}

// runReport collects the window's activity, writes one Markdown file and
// uploads it to Telegram. The file is always written before any send so a
// delivery failure never loses the data.
func runReport(ctx context.Context, cfg config.Config, days []date.Range, dryRun bool, logger *slog.Logger) int {
	client, err := gitlab.New(cfg.GitLabURL, cfg.GitLabToken)
	if err != nil {
		logger.Error("gitlab client", "error", err)
		return 1
	}

	user, err := client.CurrentUser(ctx)
	if err != nil {
		if cfg.GitLabUsername == "" {
			logger.Error("gitlab authentication", "error", err)
			return 1
		}
		logger.Warn("using GITLAB_USERNAME fallback", "error", err)
		user = &gitlab.User{Username: cfg.GitLabUsername, Name: cfg.GitLabUsername}
	}

	collector := report.NewCollector(client, logger)
	opts := report.Options{AuthorEmails: cfg.AuthorEmails}

	file, err := generate(ctx, cfg.ReportDir, collector, days, user, opts)
	if err != nil {
		logger.Error("report generation failed",
			"window", date.WindowLabel(days),
			"error", err,
		)
		return 1
	}
	// Log the absolute path: the file is often written under a relative
	// REPORT_DIR, and "reports/…" alone does not say where that is.
	displayPath := file.path
	if abs, absErr := filepath.Abs(file.path); absErr == nil {
		displayPath = abs
	}

	logger.Info("report generated",
		"date", file.stem,
		"window", file.label,
		"days", len(days),
		"path", displayPath,
		"projects", file.summary.Projects,
		"pushes", file.summary.Pushes,
		"commits", file.summary.Commits,
		"lines", file.summary.Lines(),
		"mrs", mrTotal(file.summary),
		"issues", file.summary.Issues,
		"reviews", file.summary.Reviews,
	)

	if dryRun {
		logger.Info("dry run: telegram skipped", "path", displayPath)
		return 0
	}
	if !file.summary.HasActivity() && !cfg.SendEmptyReport {
		logger.Info("no activity and SEND_EMPTY_REPORT=false: telegram skipped",
			"date", file.stem, "window", file.label)
		return 0
	}

	// Only a real send builds a client, thus neither skip branch opens a
	// socket. WithProxy("") is a direct connection, so pass the value as-is.
	tg, err := telegram.New(cfg.TelegramAPIBase, cfg.TelegramBotToken,
		telegram.WithProxy(cfg.TelegramProxy))
	if err != nil {
		logger.Error("telegram client", "error", err)
		return 1
	}

	// Upload the file under the same absolute path the logs name, so a
	// relative REPORT_DIR cannot desynchronise the two.
	res, err := tg.SendDocument(ctx, cfg.TelegramChatID, displayPath, file.caption)
	if err != nil {
		// The error is logged as returned: the client already redacts the
		// token, and extra formatting could reintroduce it. The report file
		// stays on disk.
		logger.Error("telegram delivery failed",
			"date", file.stem,
			"window", file.label,
			"path", displayPath,
			"error", err,
		)
		return 1
	}

	logger.Info("telegram delivered",
		"date", file.stem,
		"window", file.label,
		"chat_id", cfg.TelegramChatID,
		"message_id", res.MessageID,
		"path", displayPath,
	)
	return 0
}

// generate collects the window and writes its Markdown file. A one-day window
// keeps the daily document and its file name; a longer window writes the
// combined range report.
func generate(ctx context.Context, reportDir string, collector *report.Collector, days []date.Range, user *gitlab.User, opts report.Options) (generated, error) {
	if len(days) == 1 {
		rep, err := collector.Collect(ctx, days[0], user, opts)
		if err != nil {
			return generated{}, err
		}
		path, err := report.WriteFile(reportDir, rep)
		if err != nil {
			return generated{}, err
		}
		return generated{
			stem:    rep.Date,
			label:   rep.Date,
			path:    path,
			summary: rep.Summary,
			caption: caption(*rep),
		}, nil
	}

	md, err := collector.CollectWindow(ctx, days, user, opts)
	if err != nil {
		return generated{}, err
	}
	path, err := report.WriteMultiDayFile(reportDir, md)
	if err != nil {
		return generated{}, err
	}
	return generated{
		stem:    md.Stem(),
		label:   md.Label(),
		path:    path,
		summary: md.Summary,
		caption: windowCaption(md),
	}, nil
}

func mrTotal(s report.Summary) int {
	return s.MRsOpened + s.MRsMerged + s.MRsClosed + s.MRsUpdated
}

// caption is the short plain-text line that rides with the uploaded daily
// report.
func caption(rep report.Report) string {
	return captionFor("GitLab daily report "+rep.Date, rep.Summary)
}

// windowCaption is the caption for a combined window: one caption for the
// whole range, never one per day.
func windowCaption(md *report.MultiDay) string {
	return captionFor("GitLab report "+md.Label(), md.Summary)
}

// captionFor formats the shared caption tail. Zero-count parts are omitted,
// and a report without activity says so instead of listing zeros.
func captionFor(prefix string, s report.Summary) string {
	var b strings.Builder
	b.WriteString(prefix)

	if !s.HasActivity() {
		b.WriteString(" · no activity")
		return b.String()
	}
	if s.Projects > 0 {
		fmt.Fprintf(&b, " · %d %s", s.Projects, plural(s.Projects, "project"))
	}
	if s.Commits > 0 {
		fmt.Fprintf(&b, " · %d %s", s.Commits, plural(s.Commits, "commit"))
	}
	if s.Additions+s.Deletions > 0 {
		// U+2212 minus, and both numbers always show, so the pair reads as
		// one +/- figure.
		fmt.Fprintf(&b, " · +%d/\u2212%d", s.Additions, s.Deletions)
	}
	return b.String()
}

// plural returns singular for exactly one and the regular plural otherwise.
func plural(n int, singular string) string {
	if n == 1 {
		return singular
	}
	return singular + "s"
}

// loadOptions selects the env file. A file that the user names must exist. A
// missing default file is not an error, because the environment can be complete.
func loadOptions(flag string) config.LoadOptions {
	if flag != "" {
		return config.LoadOptions{EnvFile: flag, Required: true}
	}
	path, err := config.DefaultEnvFile()
	if err != nil {
		return config.LoadOptions{}
	}
	return config.LoadOptions{EnvFile: path}
}

// warnEnvFileMode tells the user when other users can read the env file. The
// file usually holds tokens, thus a mode wider than 0600 is a risk.
func warnEnvFileMode(logger *slog.Logger, path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		logger.Warn("env file is readable by other users",
			"path", path, "mode", fmt.Sprintf("%04o", perm))
	}
}
