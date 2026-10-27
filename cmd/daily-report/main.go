// Command daily-report generates the GitLab daily report and sends it to
// Telegram. Telegram delivery is wired in a later step; everything up to and
// including the Markdown file is functional.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"gitlab.daal.ir/delijan/tools/gitlab-daily-report/internal/config"
	"gitlab.daal.ir/delijan/tools/gitlab-daily-report/internal/date"
	"gitlab.daal.ir/delijan/tools/gitlab-daily-report/internal/gitlab"
	"gitlab.daal.ir/delijan/tools/gitlab-daily-report/internal/report"
)

// reportTimeout bounds the whole run; systemd's TimeoutStartSec is 300s.
const reportTimeout = 5 * time.Minute

var version = "dev"

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gitlab-daily-report", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		dateFlag    string
		dryRun      bool
		reportDir   string
		verbose     bool
		showVersion bool
	)
	fs.StringVar(&dateFlag, "date", "", "report date as YYYY-MM-DD (default: yesterday in the report timezone)")
	fs.BoolVar(&dryRun, "dry-run", false, "write the Markdown file but do not contact Telegram")
	fs.StringVar(&reportDir, "report-dir", "", "output directory (default: ./reports or REPORT_DIR)")
	fs.BoolVar(&verbose, "verbose", false, "enable debug logging")
	fs.BoolVar(&showVersion, "version", false, "print version and exit")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: gitlab-daily-report [flags]")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Environment: GITLAB_URL, GITLAB_TOKEN, GITLAB_USERNAME,")
		fmt.Fprintln(stderr, "GITLAB_AUTHOR_EMAILS, TELEGRAM_BOT_TOKEN, TELEGRAM_CHAT_ID,")
		fmt.Fprintln(stderr, "TELEGRAM_PROXY, REPORT_TIMEZONE, REPORT_DIR, SEND_EMPTY_REPORT, LOG_LEVEL")
		fmt.Fprintln(stderr)
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "unexpected argument %q\n", fs.Arg(0))
		fs.Usage()
		return 2
	}
	if showVersion {
		fmt.Fprintf(stdout, "gitlab-daily-report %s\n", version)
		return 0
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "config: %v\n", err)
		return 1
	}
	if reportDir != "" {
		cfg.ReportDir = reportDir
	}
	if verbose {
		cfg.LogLevel = slog.LevelDebug
	}
	if err := cfg.Validate(dryRun); err != nil {
		fmt.Fprintf(stderr, "config: %v\n", err)
		return 1
	}

	var rng date.Range
	if dateFlag != "" {
		rng, err = date.ParseDay(dateFlag, cfg.Location)
		if err != nil {
			fmt.Fprintf(stderr, "config: %v\n", err)
			return 1
		}
	} else {
		rng = date.Yesterday(time.Now(), cfg.Location)
	}

	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, reportTimeout)
	defer cancel()

	return runReport(ctx, cfg, rng, dryRun, logger)
}

// runReport collects the day's activity, writes the Markdown file and — once
// delivery is wired — uploads it. The file is always written before any send
// so a delivery failure never loses the data.
func runReport(ctx context.Context, cfg config.Config, rng date.Range, dryRun bool, logger *slog.Logger) int {
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
	rep, err := collector.Collect(ctx, rng, user, report.Options{AuthorEmails: cfg.AuthorEmails})
	if err != nil {
		logger.Error("collection failed", "date", rng.Date(), "error", err)
		return 1
	}

	path, err := report.WriteFile(cfg.ReportDir, rep)
	if err != nil {
		logger.Error("write report", "error", err)
		return 1
	}
	// Log the absolute path: the file is often written under a relative
	// REPORT_DIR, and "reports/…" alone does not say where that is.
	displayPath := path
	if abs, absErr := filepath.Abs(path); absErr == nil {
		displayPath = abs
	}

	logger.Info("report generated",
		"date", rep.Date,
		"path", displayPath,
		"projects", rep.Summary.Projects,
		"pushes", rep.Summary.Pushes,
		"commits", rep.Summary.Commits,
		"lines", rep.Summary.Lines(),
		"mrs", mrTotal(rep.Summary),
		"issues", rep.Summary.Issues,
		"reviews", rep.Summary.Reviews,
	)

	if dryRun {
		logger.Info("dry run: telegram skipped", "path", displayPath)
		return 0
	}
	if !rep.Summary.HasActivity() && !cfg.SendEmptyReport {
		logger.Info("no activity and SEND_EMPTY_REPORT=false: telegram skipped", "date", rep.Date)
		return 0
	}

	logger.Error("telegram delivery is not implemented yet", "date", rep.Date, "path", displayPath)
	return 1
}

func mrTotal(s report.Summary) int {
	return s.MRsOpened + s.MRsMerged + s.MRsClosed + s.MRsUpdated
}
