#!/usr/bin/env bash
# Manual runner: sources the env file, then executes the binary.
#
#   ./run.sh --date 2026-10-27 --dry-run
#
# Override the env file with ENV_FILE=..., the binary with BIN=...
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
env_file="${ENV_FILE:-${XDG_CONFIG_HOME:-$HOME/.config}/gitlab-daily-report/env}"
bin="${BIN:-$root/bin/gitlab-daily-report}"

if [[ ! -x "$bin" ]]; then
    echo "run.sh: no binary at $bin" >&2
    echo "run.sh: build it with: go build -o bin/gitlab-daily-report ./cmd/daily-report" >&2
    exit 1
fi

if [[ -f "$env_file" ]]; then
    set -a
    # shellcheck disable=SC1090
    source "$env_file"
    set +a
else
    echo "run.sh: warning: env file $env_file not found, using the current environment" >&2
    if [[ -z "${GITLAB_TOKEN:-}" ]]; then
        echo "run.sh: GITLAB_TOKEN is not set either. Create the env file:" >&2
        echo "  install -d -m 700 \"$(dirname "$env_file")\"" >&2
        echo "  install -m 600 \"$root/.env.example\" \"$env_file\"" >&2
        echo "  \$EDITOR \"$env_file\"" >&2
    fi
fi

exec "$bin" "$@"
