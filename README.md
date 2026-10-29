# punchcard

Generates a deterministic English Markdown report of yesterday's GitLab
activity for one user and sends it to Telegram as a document.

- Instance: `https://gitlab.daal.ir`
- Report day: yesterday in `Asia/Tehran` by default
- Data: commits/pushes, merge requests, reviews, issues and issue comments
- Output: `reports/YYYY-MM-DD.md`, mode `0600`

## Status

Working:

- `--dry-run`: collects the day from GitLab, renders `reports/YYYY-MM-DD.md`,
  applies the exit codes and logs to stderr via `slog`
- configuration, `Asia/Tehran` date math, GitLab client, collection,
  deduplication and the Markdown renderer
- `punchcard update`: replaces the binary with the latest GitHub release
- Telegram delivery: a non-dry-run uploads `reports/YYYY-MM-DD.md` to
  `TELEGRAM_CHAT_ID` with `sendDocument` and a short caption, exits 0 after
  the API confirms, and exits 1 without deleting the file when delivery fails

Pending:

- systemd units and deployment docs

## Quick start

```bash
# Requires Go 1.27+
make build

mkdir -p ~/.config/punchcard
cp .env.example ~/.config/punchcard/env
chmod 600 ~/.config/punchcard/env
$EDITOR ~/.config/punchcard/env

./bin/punchcard --date 2026-10-27 --dry-run
```

The binary reads the env file itself. It reads
`$XDG_CONFIG_HOME/punchcard/env`, or `~/.config/punchcard/env` when
`XDG_CONFIG_HOME` is empty. Use `--env-file PATH` to name another file.

## Configuration

The configuration is still a set of environment variables. The binary reads
the `KEY=VALUE` lines from the env file, thus a manual run and a systemd timer
use the same file and the same parser.

The lookup order has four levels, highest first:

1. Flags: `--report-dir` and `--verbose`.
2. The process environment.
3. The env file.
4. The defaults in the table below.

The process environment always wins over the file. A missing default file is
not an error, because the environment can be complete. A file that you name
with `--env-file` must exist, else the binary exits 2.

The parser reads data only. It accepts `KEY=VALUE` lines, an optional `export`
prefix, `#` comments, and one pair of surrounding quote marks. It does not
expand `$VAR` and it does not run commands.

| Variable | Required | Default | Purpose |
|---|---:|---|---|
| `GITLAB_URL` | no | `https://gitlab.daal.ir` | GitLab base URL |
| `GITLAB_TOKEN` | yes | — | personal access token (`read_api` + `read_user`) |
| `GITLAB_USERNAME` | no | from `/user` | fallback username |
| `GITLAB_AUTHOR_EMAILS` | no | empty | comma-separated commit-author filter |
| `TELEGRAM_BOT_TOKEN` | yes unless `--dry-run` | — | BotFather token |
| `TELEGRAM_CHAT_ID` | yes unless `--dry-run` | — | numeric chat id |
| `TELEGRAM_PROXY` | no | empty | `socks5h://127.0.0.1:1089` on the laptop |
| `TELEGRAM_API_BASE` | no | `https://api.telegram.org` | Bot API base; change only for a self-hosted server or tests |
| `REPORT_TIMEZONE` | no | `Asia/Tehran` | day boundaries and display time |
| `REPORT_DIR` | no | `./reports` | where Markdown files are written |
| `SEND_EMPTY_REPORT` | no | `true` | send a no-activity heartbeat |
| `LOG_LEVEL` | no | `info` | `debug`, `info`, `warn`, `error` |

Flags: `-d/--date YYYY-MM-DD`, `--env-file PATH`, `--dry-run`,
`--report-dir PATH`, `-v/--verbose`, `-V/--version`, `-h/--help`.
Keep the long forms in scripts and systemd units.

Commands: `update` (see below), `completion SHELL`, `help`. A bare `punchcard`
runs the report.

## Telegram delivery

A run always writes the report first. It then uploads the same file to one
chat, unless the run is a dry run.

### What is sent

The `.md` as a document named `YYYY-MM-DD.md` with `text/markdown`, plus one
plain-text caption and no `parse_mode`:

```text
GitLab daily report 2026-10-27 · 3 projects · 12 commits · +240/−80
```

Zero-count parts are left out. A day without activity sends
`GitLab daily report <date> · no activity`, so `SEND_EMPTY_REPORT=true` still
produces a heartbeat. `--dry-run` and `SEND_EMPTY_REPORT=false` write the file
and skip the upload.

### Create the bot and find the chat

1. Run `/newbot` in [BotFather](https://t.me/BotFather) and put the token in
   `TELEGRAM_BOT_TOKEN`.
2. Give the bot a chat. It cannot start one itself, thus the recipient must
   send `/start` to the bot, or you must add the bot to a group.
3. Read the numeric `chat_id` from `getUpdates` on `TELEGRAM_API_BASE`:

   ```bash
   curl -s "https://api.telegram.org/bot<token>/getUpdates"
   ```

   The id is negative for a group. Set it as `TELEGRAM_CHAT_ID`.

The token and the chat id are required only for a real send. `--dry-run` needs
neither, but `TELEGRAM_API_BASE` and `TELEGRAM_PROXY` are still validated.

### Limits

The Bot API accepts 50 MB per document and 1024 bytes per caption. Both are
checked before the upload starts, so an oversized file is rejected, not sent.

### Proxy

`TELEGRAM_PROXY` accepts `socks5`, `socks5h`, `http`, and `https`. An empty
value means a direct connection, even when `HTTPS_PROXY` is set.

### Self-hosted Bot API

`TELEGRAM_API_BASE` defaults to `https://api.telegram.org` and needs a change
only for a self-hosted Bot API server or a test server. The value is trimmed,
takes no `/bot…` suffix, and must be an `http` or `https` URL with a host.

### Failures

A 429, a 5xx response, or a transport error is retried, up to three attempts
in total; a 429's `retry_after` replaces the default backoff, and any other
4xx fails at once. The error is logged with the bot token redacted, the report
stays on disk, and the run exits 1 so a scheduled job reports the failure.

## How the report is built

1. `GET /user` identifies the user (cached for the process).
2. `GET /events` for the report day yields pushes, issues and issue comments.
3. Push events expand to commits through `/repository/commits/:sha`,
   `/repository/compare`, or a `ref_name` listing as a fallback.
4. Commits are deduplicated by `project_id + sha`; refs and the earliest push
   time are merged. Line stats come from the single-commit endpoint.
5. `/merge_requests` is queried twice (author and reviewer). Notes are fetched
   for at most 50 MRs per day and filtered to the user's non-system notes
   inside the day.
6. Project metadata is fetched once per project. Unavailable projects render
   as `project #<id>` without links.
7. The renderer sorts projects by path, commits by push time, and omits empty
   sections. A day without activity renders a short `No activity` report.

The summary is computed from the same model that feeds the sections, so the
counts and the body can never drift apart.

## Layout

```text
cmd/punchcard/          cobra commands, flags, wiring, exit codes
internal/config/       env file, environment parsing and validation
internal/date/         Asia/Tehran ranges and GitLab date parameters
internal/gitlab/       HTTP client, endpoints, types
internal/report/       collection, normalization, Markdown
internal/telegram/     Bot API client: upload, limits, retry
internal/update/       GitHub release client and binary replacement
internal/testutil/     fixture loader, fake GitLab and Telegram servers
testdata/              sanitized API fixtures
.github/workflows/     ci and release
.goreleaser.yaml       release targets, archives, checksums
```

## Build

```bash
make build       # bin/punchcard, stamped with version, commit and date
make install     # go install into GOBIN
make run         # dry run with the fresh binary; ARGS is forwarded
make completion  # shell completion for bash, zsh or fish
make clean       # remove bin/
```

`make build` also installs shell completion for the current `$SHELL`. The
binary reports its build data:

```text
$ ./bin/punchcard --version
punchcard 003e46c-dirty (commit 003e46c, built 2026-10-28T19:16:56Z, linux/amd64)
```

Forward arguments to a manual run with `ARGS`:

```bash
make run ARGS="--date 2026-10-27"
```

## Update

`punchcard update` replaces the running binary with the latest release:

```bash
punchcard update --check   # report the newest version, install nothing
punchcard update           # install it and print the release notes from
```

The updater reads `https://api.github.com/repos/arash-jafarpour/punchcard`.
It selects `punchcard_<os>_<arch>.tar.gz`, or `.zip` on Windows. It verifies
the archive against the `<asset>.sha256` sidecar before it replaces the binary,
so a bad download leaves the old binary in place.

`GITHUB_TOKEN` is optional. The repository is public, thus the token only
raises the API rate limit.

A development build reports `dev` and cannot update itself. Install from a
release to enable the command.

## Releases

A `v*` tag starts the release job:

```bash
git tag -a v0.1.0 -m "punchcard v0.1.0"
git push origin v0.1.0
```

GoReleaser builds six targets (linux, darwin and windows, on amd64 and arm64),
writes one archive per target with a `.sha256` sidecar, and publishes the
release notes from the Conventional Commits since the last tag. You can also
start the job by hand from the Actions tab with an existing tag.

`dist/` holds the local build output and is not committed.

## Tests

```bash
make check   # gofmt check, vet, tests
make race    # tests with the race detector
make build   # bin/punchcard, stamped with the git description
```

The targets call these commands:

```bash
gofmt -l .
go vet ./...
go test ./... -count=1
go test -race ./...
```

Tests use fixtures and `httptest`, never the live GitLab instance. Regenerate
the rendered golden file after an intentional format change:

```bash
go test ./internal/report -run TestRenderGolden -update
```

## Known limitations

- `/events` is queried without `scope=all`. On this instance `scope=all`
  selects a capped recent-activity feed (20 newest events), which silently
  truncates older parts of a day once newer events arrive; the default feed is
  complete, per-user and paginates normally. Events are still filtered by
  `author_username` as a guard.
- A push can still carry commits authored by someone else (for example when
  you merge or push a colleague's branch). Those commits are reported with a
  `_(by …)_` suffix and are counted in the summary. Set `GITLAB_AUTHOR_EMAILS`
  to restrict commits to your own addresses if you prefer a stricter report.
- MR approvals are not enriched. GitLab does not expose a reliable per-approval
  timestamp through the approvals endpoint, so v1 omits the "approved" marker
  rather than guessing.
- The ref-listing fallback (`commits?ref_name=...&since=...&until=...`) is not
  scoped to one author and can surface commits that were pushed later. Its
  push time is treated as approximate; a later push event that names the same
  commit wins.
- `Persistent=true` timers still default to "yesterday relative to now", so a
  laptop that was off for several days produces one report, not one per missed
  day.
- Delivery has no persistence or deduplication. A retry after a timeout can
  deliver the same file twice, and a run that fails leaves the report on disk
  for a manual resend.
