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

Pending:

- Telegram `sendDocument` and the SOCKS5/HTTP proxy transport (a non-dry-run
  currently writes the file, then exits 1 with `telegram delivery is not
  implemented yet`)
- systemd units and deployment docs

## Quick start

```bash
# Requires Go 1.27+
go build -o bin/punchcard ./cmd/punchcard

mkdir -p ~/.config/punchcard
cp .env.example ~/.config/punchcard/env
chmod 600 ~/.config/punchcard/env
$EDITOR ~/.config/punchcard/env

./run.sh --date 2026-10-27 --dry-run
```

`run.sh` sources `${XDG_CONFIG_HOME:-$HOME/.config}/punchcard/env`
before executing `bin/punchcard`.

## Configuration

All configuration comes from the environment; no `.env` parser is bundled.

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

Flags: `--date YYYY-MM-DD`, `--dry-run`, `--report-dir PATH`, `--verbose`,
`--version`, `--help`.

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
cmd/punchcard/          flags, wiring, exit codes
internal/config/       environment parsing and validation
internal/date/         Asia/Tehran ranges and GitLab date parameters
internal/gitlab/       HTTP client, endpoints, types
internal/report/       collection, normalization, Markdown
internal/testutil/     fixture loader and fake GitLab server
testdata/              sanitized API fixtures
```

## Tests

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
