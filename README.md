# FixFlow

Autonomous issue-to-code daemon. FixFlow watches your GitLab, GitHub, and Sentry issues,
then uses an LLM (Claude or Codex CLI) to plan, implement, test, and push fixes — ready
for human approval.

## How It Works

```
                              ┌─────────────┐
  GitLab webhook ────────────>│             │
  GitHub/Sentry sync loop ──>│  ff daemon   │──> clone repo, create branch
                              │             │    plan → implement → review → test
                              └──────┬──────┘
                                     │
                              ┌──────▼──────┐
                              │   SQLite    │  jobs, sessions, artifacts
                              └──────┬──────┘
                                     │
                              ┌──────▼──────┐
                              │  LLM CLI    │  claude --print / codex --full-auto
                              └─────────────┘
```

**Pipeline per issue:**

1. **Plan** — LLM analyzes the issue and produces an implementation plan.
2. **Implement** — LLM writes code in a git worktree following the plan.
3. **Code Review** — LLM reviews its own changes. If not approved, loops back to implement (up to `max_iterations`).
4. **Test** — Runs the project's test command. On pass, pushes the branch.
5. **Ready** — Waits for human `approve` / `reject` via CLI or TUI.

## Quick Start

```bash
# Build
go build -o ff ./cmd/fixflow

# Initialize config + database
./ff init

# Edit the config
./ff config   # opens fixflow.toml in $EDITOR

# Set tokens via env vars (never commit these)
export GITLAB_TOKEN="glpat-..."
export GITHUB_TOKEN="ghp_..."
export SENTRY_TOKEN="sntrys_..."
export FIXFLOW_WEBHOOK_SECRET="your-secret"

# Start the daemon
./ff start

# Or run in foreground for debugging
./ff start -f
```

## Configuration

FixFlow uses a single `fixflow.toml` file. Running `ff init` creates a starter template.

```toml
db_path = "fixflow.db"
repos_root = ".repos"
log_level = "info"         # debug, info, warn, error
# log_file = "fixflow.log" # uncomment to log to file

[daemon]
webhook_port = 8080
max_workers = 3
max_iterations = 3         # implement<->review retries
sync_interval = "5m"       # GitHub/Sentry polling interval
pid_file = "fixflow.pid"

[tokens]
# Prefer env vars: GITLAB_TOKEN, GITHUB_TOKEN, SENTRY_TOKEN

[sentry]
base_url = "https://sentry.io"

[llm]
provider = "claude"        # claude or codex

[[projects]]
name = "my-project"
repo_url = "git@gitlab.com:org/repo.git"
test_cmd = "make test"
base_branch = "main"

  [projects.gitlab]
  base_url = "https://gitlab.com"
  project_id = "12345"

  # [projects.github]
  # owner = "org"
  # repo = "repo"

  # [projects.sentry]
  # org = "my-org"
  # project = "my-project"

  # [projects.prompts]
  # plan = "prompts/plan.md"
  # implement = "prompts/implement.md"
  # code_review = "prompts/code_review.md"
```

### Environment Variable Overrides

| Env Var | Overrides |
|---------|-----------|
| `GITLAB_TOKEN` | `[tokens] gitlab` |
| `GITHUB_TOKEN` | `[tokens] github` |
| `SENTRY_TOKEN` | `[tokens] sentry` |
| `FIXFLOW_WEBHOOK_SECRET` | `[daemon] webhook_secret` |

## Setting Up a Project

### GitLab (webhook-driven)

1. Add a `[[projects]]` block with `[projects.gitlab]` containing your `project_id`.
2. In GitLab, go to **Settings > Webhooks** and add:
   - **URL:** `http://<your-host>:8080/webhook`
   - **Secret token:** same value as `FIXFLOW_WEBHOOK_SECRET`
   - **Trigger:** Issue events
3. When an issue is opened or reopened, FixFlow creates a job automatically.

### GitHub (polling)

1. Add `[projects.github]` with `owner` and `repo`.
2. FixFlow polls for open issues every `sync_interval`.
3. New issues are picked up and processed automatically.

### Sentry (polling)

1. Add `[projects.sentry]` with `org` and `project`.
2. FixFlow polls for unresolved issues every `sync_interval`.

## CLI Commands

| Command | Description |
|---------|-------------|
| `ff init` | Create config template and initialize database |
| `ff start [-f]` | Start the daemon (`-f` for foreground) |
| `ff stop` | Gracefully stop the daemon |
| `ff status` | Show daemon status and job counts by state |
| `ff list [--project X] [--state Y]` | List jobs with optional filters |
| `ff logs <job-id>` | Show full session history, artifacts, and tokens |
| `ff approve <job-id>` | Approve a job in `ready` state |
| `ff reject <job-id> [-r reason]` | Reject a job in `ready` state |
| `ff retry <job-id> [-n notes]` | Re-queue a `failed` or `rejected` job |
| `ff config` | Open `fixflow.toml` in `$EDITOR` |
| `ff tui` | Interactive terminal dashboard |

All commands accept `--json` for machine-readable output and `-v` for debug logging.

## Job States

```
queued → planning → implementing → reviewing → testing → ready
                        ↑              │
                        └──────────────┘  (review requested changes)

ready → approved    (human approves)
ready → rejected    (human rejects)
any   → failed      (error)
failed/rejected → queued  (ff retry)
```

## Custom Prompts

Override default prompts per project in `fixflow.toml`:

```toml
[projects.prompts]
plan = "prompts/plan.md"
implement = "prompts/implement.md"
code_review = "prompts/code_review.md"
```

Prompt templates support these placeholders:

| Placeholder | Value |
|-------------|-------|
| `{{title}}` | Issue title |
| `{{body}}` | Issue body (sanitized) |
| `{{plan}}` | Plan artifact content |
| `{{review_feedback}}` | Previous review + test output |

## Architecture

```
cmd/fixflow/           CLI (cobra)
internal/
  config/              TOML config loader with env overrides
  daemon/              Daemon lifecycle, PID file, signal handling
  db/                  SQLite store (WAL mode, reader/writer pools)
  git/                 Clone, branch, worktree, push operations
  issuesync/           GitHub + Sentry polling sync loop
  llm/                 CLI provider interface (claude, codex)
  pipeline/            Plan → implement → review → test orchestration
  tui/                 Bubbletea interactive dashboard
  webhook/             GitLab webhook handler
  worker/              Concurrent job processing pool
```

## Requirements

- Go 1.23+
- `claude` CLI or `codex` CLI on `$PATH`
- Git
- SQLite (via `modernc.org/sqlite`, no CGO required)

## Development

```bash
go build ./...
go vet ./...
go test ./...
```

## Resetting the Database

```bash
rm -f fixflow.db
./ff init   # re-creates schema
```
