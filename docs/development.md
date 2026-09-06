# Development Guide

## Prerequisites

| Tool | Version | Purpose |
|---|---|---|
| Go | 1.26.0 | All agent adapters, ProofGate engine, e2e tests |
| mise | latest | Task runner for build, test, lint |
| golangci-lint | 2.11.3 | Go linting (52 linters enabled) |
| tmux | latest | E2E lifecycle tests (interactive agent sessions) |
| Python 3 | latest | Control Room app (if working on `proofgate/app/`) |
| Databricks CLI | latest | Bundle deployment (if deploying to Databricks) |

### Install mise

```bash
curl https://mise.run | sh
# or
brew install mise
```

### Install tools via mise

```bash
# From repo root — installs Go, golangci-lint, tmux
mise install
```

## Building

### Build all agents

```bash
mise run build
```

This discovers all `agents/entire-agent-*` directories, builds each binary, and copies them to `./bin/`.

> **Note:** The `build`, `test`, and `lint` tasks are defined as file-based tasks in `mise-tasks/` (not in `mise.toml`). mise discovers them automatically. The root `mise.toml` defines `fmt`, `test:e2e`, `test:e2e:lifecycle`, and `test:ci`.

### Build a specific agent

```bash
mise run build agents/entire-agent-github-actions
# or directly
cd agents/entire-agent-github-actions && go build -o entire-agent-github-actions ./cmd/entire-agent-github-actions
```

### Build ProofGate

```bash
cd proofgate && go build ./cmd/proofgate
```

## Running tests

### Unit tests (all agents)

```bash
mise run test
```

### Test a specific agent

```bash
mise run test agents/entire-agent-github-actions
# or directly
cd agents/entire-agent-github-actions && go test ./...
```

### ProofGate tests

```bash
cd proofgate && go test ./...
```

### E2E lifecycle tests

Requires `tmux` and agent CLI binaries in PATH.

```bash
# All agents
mise run test:e2e

# Specific agent
E2E_AGENT=kiro mise run test:e2e

# Keep temp repos for debugging
E2E_KEEP_REPOS=1 mise run test:e2e
```

### Protocol compliance tests

Run locally via the shared compliance suite:

```bash
cd agents/entire-agent-github-actions
go build -o entire-agent-github-actions ./cmd/entire-agent-github-actions
./scripts/verify-compliance.sh ./entire-agent-github-actions
```

## Linting

### Run all linters

```bash
mise run lint
```

### Lint a specific module

```bash
cd agents/entire-agent-github-actions && golangci-lint run
```

### Format code

```bash
mise run fmt
```

## Adding a new agent adapter

1. Create the directory structure:
```
agents/entire-agent-{name}/
├── cmd/entire-agent-{name}/main.go
├── internal/{name}/
│   ├── agent.go
│   ├── hooks.go
│   └── transcript.go
├── internal/protocol/
│   ├── protocol.go
│   └── types.go
├── go.mod
├── mise.toml
├── AGENT.md
└── README.md
```

2. Implement the required interfaces (see `agents/entire-agent-github-actions/` as a reference):
   - `Info()` → return agent name, capabilities, protected files
   - `Detect()` → return whether the agent environment is present
   - `GetSessionDir()` → return session storage directory
   - `ParseHook()` → parse lifecycle events from stdin
   - Transcript analysis methods if declaring `transcript_analyzer` capability

3. Register the agent in `e2e/agents/`:
   - Create `{name}.go` implementing the `Agent` interface
   - Register in `init()` with concurrency gate

4. Add to `mise.toml` if custom build/test tasks are needed.

5. The CI workflows auto-discover agents from the `agents/` directory — no workflow changes needed.

## Project conventions

### Code style

- Go standard formatting (`gofmt -s`)
- 52 golangci-lint linters enabled (see `.golangci.yaml`)
- Test files excluded from: errchkjson, gosec, goconst, revive, unparam
- `e2e/` directory has relaxed linting (errcheck, gosec exemptions)

### Naming

- Agent binaries: `entire-agent-{name}`
- Go modules: `github.com/entireio/external-agents/agents/entire-agent-{name}`
- Hook names: `run-start`, `turn-start`, `turn-end`, `run-end`
- Session directories: scoped per-repository via SHA256 hash

### Error handling

- CLI commands exit with code 1 on error, writing to stderr
- JSON responses use standard Go `encoding/json`
- Transcript parsing handles incomplete/malformed input gracefully
- The policy engine returns errors for invalid passports (missing fields, invalid ranges)

### File operations

- Session files: atomic write (temp + rename), `0o600` permissions
- Hook files: managed marker (`# Managed by entire-agent-github-actions`), safety check before overwrite
- Transcript storage: raw bytes preserved exactly as received

## Environment variables reference

### E2E tests

| Variable | Default | Purpose |
|---|---|---|
| `E2E_AGENT` | all | Filter to single agent |
| `E2E_ENTIRE_BIN` | `entire` from PATH | Override Entire binary path |
| `E2E_ARTIFACT_DIR` | `e2e/artifacts/<timestamp>` | Override artifact output |
| `E2E_KEEP_REPOS` | unset | Preserve temp repos after tests |
| `E2E_CONCURRENT_TEST_LIMIT` | agent-specific | Override concurrency gate |
| `GOCACHE` | `/tmp/go-build-cache` | Go build cache location |

### GitHub Actions agent

| Variable | Purpose |
|---|---|
| `GITHUB_ACTIONS` | Detection trigger (must be `true`) |
| `RUNNER_TEMP` | Session directory preference |
| `ENTIRE_REPO_ROOT` | Repository root override |
| `ENTIRE_CLI_PATH` | Entire binary override |
| `GITHUB_REPOSITORY` | Metadata: repository name |
| `GITHUB_RUN_ID` | Metadata: workflow run ID |
| `GITHUB_RUN_ATTEMPT` | Metadata: attempt number |
| `GITHUB_SHA` | Metadata: commit SHA |
| `GITHUB_WORKFLOW` | Metadata: workflow name |
| `GITHUB_JOB` | Metadata: job name |

### ProofGate / Databricks

| Variable | Purpose |
|---|---|
| `DATABRICKS_HOST` | Databricks workspace URL |
| `DATABRICKS_TOKEN` | API authentication token |
| `DATABRICKS_SQL_WAREHOUSE_ID` | SQL warehouse ID |
| `PROOFGATE_DATABRICKS_CATALOG` | Unity Catalog name |
| `PROOFGATE_DATABRICKS_SCHEMA` | Schema name |

## IDE integration

The repository includes configuration for four IDEs:

| IDE | Config location | Entry point |
|---|---|---|
| Claude Code | `.claude/skills/entire-external-agent/` | `SKILL.md` |
| Codex | `.codex/`, `AGENTS.md` | `AGENTS.md` |
| Cursor | `.cursor/rules/` | `entire-external-agent.mdc` |
| OpenCode | `.opencode/plugins/` | `entire.ts` |

Each IDE integration provides agent building guidance (research → write-tests → implement) using the same underlying skill definitions.
