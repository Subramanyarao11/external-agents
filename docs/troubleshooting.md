# Troubleshooting

## Common issues

### Agent not detected

**Symptom:** `entire enable --agent github-actions` fails or agent not found.

**Causes and fixes:**

1. **Binary not in PATH**
   ```bash
   which entire-agent-github-actions
   # If not found:
   mise run build agents/entire-agent-github-actions
   cp agents/entire-agent-github-actions/entire-agent-github-actions /usr/local/bin/
   ```

2. **External agents not enabled**
   ```bash
   # Check .entire/settings.json
   cat .entire/settings.json | grep external_agents
   # Should contain: "external_agents": true
   ```

3. **Wrong environment** (GitHub Actions agent)
   - `detect` returns `true` only when `GITHUB_ACTIONS=true`
   - Outside GitHub Actions, the agent reports as not present

### Entire hooks out of date

**Symptom:** `entire status` shows `! Claude Code hooks out of date`.

**Fix:**
```bash
entire enable --force
```

### ProofGate evaluation errors

**Symptom:** `go run ./cmd/proofgate evaluate` returns an error.

| Error | Cause | Fix |
|---|---|---|
| `unsupported schema version` | Passport `schema_version` is not `1.0` | Use `schema_version: "1.0"` |
| `event_id is required` | Missing `event_id` in passport | Add a unique `event_id` |
| `repository.id is required` | Missing `repository.id` | Add repository identifier |
| `pass_maximum must be less than warn_maximum` | Invalid custom policy | Fix threshold ordering |
| `failure rates must be between 0 and 1` | Invalid `similar_failure_rate` or `component_failure_rate` | Use values in [0, 1] range |

### Databricks export failures

**Symptom:** `go run ./cmd/proofgate export` fails.

| Error | Cause | Fix |
|---|---|---|
| `host must use HTTPS` | HTTP URL provided | Use `https://` prefix |
| `all config fields are required` | Missing env var | Set all 5 Databricks env vars |
| `Databricks API returned 4xx/5xx` | Auth failure or bad request | Verify token, warehouse ID, catalog, schema |
| `decode Databricks response` | Unexpected API response | Check warehouse status in Databricks UI |

**Verify connectivity:**
```bash
# Test with dry-run first (no Databricks needed)
go run ./cmd/proofgate export --passport examples/pass.json --dry-run --now 2026-09-04T12:00:00Z
```

### Transcript parsing issues

**Symptom:** Transcript analysis returns unexpected results.

**Format detection:**
The parser (`parseSDKMessages` in `transcript.go`) auto-detects format:
- Starts with `[` → JSON array (legacy)
- Single JSON object with `messages` → envelope format
- Single JSON object with `event` → single event
- Otherwise → JSONL (one event per line)

**Incomplete JSONL:**
- Truncated last line → returns all complete records (no error)
- Entirely incomplete (only partial first record) → returns empty result (no error)
- Malformed complete line → returns error

**Debug transcript parsing:**
```bash
# Check position (message count)
entire-agent-github-actions get-transcript-position --path /path/to/transcript.jsonl

# Extract files
entire-agent-github-actions extract-modified-files --path /path/to/transcript.jsonl --offset 0
```

### E2E test failures

**Symptom:** `mise run test:e2e` fails.

| Issue | Fix |
|---|---|
| `tmux: command not found` | Install tmux: `brew install tmux` or `mise install` |
| `entire: command not found` | Install Entire CLI or set `E2E_ENTIRE_BIN` |
| Transient API failures | Tests auto-retry up to 3 times; check `console.log` for `[transient]` markers |
| Agent binary not found | Run `mise run build` first |
| `RunPrompt not implemented` | Expected for GitHub Actions (hosted-only agent) |

**Debug a failing test:**
```bash
# Run single agent with repo preservation
E2E_AGENT=kiro E2E_KEEP_REPOS=1 mise run test:e2e

# Check artifacts
ls e2e/artifacts/latest/
cat e2e/artifacts/latest/TestLifecycle_*/console.log
```

### Hook dispatch failures

**Symptom:** Lifecycle hooks not dispatched or errors during capture.

**Verify Entire CLI:**
```bash
which entire
entire status
```

**Override CLI path:**
```bash
export ENTIRE_CLI_PATH=/path/to/entire
```

**Check hook dispatch manually:**
```bash
echo '{"hook_type":"run-start","session_id":"test-001","timestamp":"2026-09-06T12:00:00Z"}' | \
  entire-agent-github-actions parse-hook --hook run-start
```

### Build failures

**Symptom:** `go build` or `mise run build` fails.

| Issue | Fix |
|---|---|
| Wrong Go version | Use Go 1.26.0: `mise install go@1.26.0` |
| Missing dependencies | `go mod download` in the module directory |
| Sandbox loopback errors | ProofGate warehouse tests use `httptest` — ensure loopback access |

### Lint failures

**Symptom:** `golangci-lint run` reports issues.

```bash
# Check specific linter
golangci-lint run --enable-only gosec

# See full issue details
golangci-lint run -v

# Auto-fix where possible
golangci-lint run --fix
```

**Common lint suppressions** (must include explanation per `.golangci.yaml`):
```go
//nolint:gosec // G204: subprocess arguments are controlled by the caller, not user input
```

### Git diff --check whitespace warnings

**Symptom:** `git diff --check main..HEAD` reports blank line at EOF.

These are pre-existing whitespace defects in the archived baseline commits. They were documented in `BUILDATHON.md` and are not modified by new work.

**Affected files (batch 1):**
- `agents/entire-agent-github-actions/scripts/verify-github-actions.sh`

**Affected files (batch 2):**
- `proofgate/app/.gitignore`
- `proofgate/app/explainer.py`
- `proofgate/app/similarity.py`
- `proofgate/app/warehouse_sync.py`
- `proofgate/databricks/phase2/README.md`
- `proofgate/databricks/phase2/pipeline/proofgate_features.py`
- `proofgate/examples/test-report.json`

## Diagnostic commands

```bash
# Check Entire status
entire status

# Check Graph version
entire graph version

# Verify agent protocol
entire-agent-github-actions info | python3 -m json.tool

# Check agent detection
GITHUB_ACTIONS=true entire-agent-github-actions detect

# Verify bundle
git bundle verify /path/to/proofgate-implementation.bundle

# Run ProofGate dry evaluation
cd proofgate && go run ./cmd/proofgate evaluate --passport examples/pass.json --now 2026-09-04T12:00:00Z

# Check default policy
cd proofgate && go run ./cmd/proofgate default-policy | python3 -m json.tool
```
