// Command punchcard generates the GitLab daily report and sends it to
// Telegram. Telegram delivery is wired in a later step; everything up to and
// including the Markdown file is functional.
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
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/arash-jafarpour/punchcard/internal/config"
	"github.com/arash-jafarpour/punchcard/internal/date"
	"github.com/arash-jafarpour/punchcard/internal/gitlab"
	"github.com/arash-jafarpour/punchcard/internal/report"
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
	envFile   string
	dryRun    bool
	reportDir string
	verbose   bool
}

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

	fmt.Fprintf(stderr, "Error: %v\n", err)
	if !started {
		// The command line did not parse. Show the usage text.
		fmt.Fprintln(stderr)
		fmt.Fprint(stderr, cmd.UsageString())
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
		Long: "Generate a Markdown report of one user's GitLab activity for one day.\n" +
			"Telegram delivery is not implemented yet.\n\n" +
			"Precedence: flags, then the process environment, then the env file.\n" +
			"The env file holds KEY=VALUE lines and defaults to\n" +
			"$XDG_CONFIG_HOME/punchcard/env.",
		Example: "  punchcard --date 2026-10-27 --dry-run\n" +
			"  punchcard --env-file ./env --verbose",
		Version:       versionString(),
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			*started = true
			return prepare(cmd, opts)
		},
	}
	cmd.SetVersionTemplate("{{.Name}} {{.Version}}\n")

	flags := cmd.Flags()
	flags.StringVarP(&opts.date, "date", "d", "", "report date as YYYY-MM-DD (default: yesterday in the report timezone)")
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

	var rng date.Range
	if opts.date != "" {
		rng, err = date.ParseDay(opts.date, cfg.Location)
		if err != nil {
			return err
		}
	} else {
		rng = date.Yesterday(time.Now(), cfg.Location)
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

	if code := runReport(ctx, cfg, rng, opts.dryRun, logger); code != 0 {
		return errRunFailed
	}
	return nil
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
