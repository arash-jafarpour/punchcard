# punchcard

Generates a deterministic English Markdown report of yesterday's GitLab
activity for one user and (eventually) sends it to Telegram as a document.

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

Pending:

- Telegram `sendDocument` and the SOCKS5/HTTP proxy transport (a non-dry-run
  currently writes the file, then exits 1 with `telegram delivery is not
  implemented yet`)
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
| `REPORT_TIMEZONE` | no | `Asia/Tehran` | day boundaries and display time |
| `REPORT_DIR` | no | `./reports` | where Markdown files are written |
| `SEND_EMPTY_REPORT` | no | `true` | send a no-activity heartbeat |
| `LOG_LEVEL` | no | `info` | `debug`, `info`, `warn`, `error` |

Flags: `-d/--date YYYY-MM-DD`, `--env-file PATH`, `--dry-run`,
`--report-dir PATH`, `-v/--verbose`, `-V/--version`, `-h/--help`.
Keep the long forms in scripts and systemd units.

Commands: `update` (see below), `completion SHELL`, `help`. A bare `punchcard`
runs the report.

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
internal/update/       GitHub release client and binary replacement
internal/testutil/     fixture loader and fake GitLab server
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
- Telegram delivery is not implemented yet; the SOCKS5 proxy must be reachable
  when the scheduled run fires once it is.
