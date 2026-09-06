# API Reference

## ProofGate CLI

The ProofGate CLI (`proofgate/cmd/proofgate/main.go`) provides subcommands for evaluating change passports, exporting evidence, and CI integration.

### `evaluate`

Evaluates a Change Passport against a risk policy and outputs the decision.

```bash
go run ./cmd/proofgate evaluate [flags]
```

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--passport` | `-` (stdin) | Path to passport JSON file |
| `--policy` | (default policy) | Path to custom policy JSON file |
| `--now` | current time | Evaluation time in RFC3339 format (for reproducibility) |

**Output:** JSON `Result` object

```json
{
  "decision": "PASS",
  "score": 0,
  "policy_version": "proofgate-default-v1",
  "passport_fingerprint": "sha256:6e9c96dc...",
  "reasons": [],
  "hard_stops": [],
  "uncertainties": [],
  "recommended_review_focus": [],
  "evaluated_at": "2026-09-04T12:00:00Z"
}
```

**Examples:**

```bash
# Evaluate a passing passport
go run ./cmd/proofgate evaluate \
  --passport examples/pass.json \
  --now 2026-09-04T12:00:00Z

# Evaluate with custom policy
go run ./cmd/proofgate evaluate \
  --passport examples/approval-required.json \
  --policy custom-policy.json

# Pipe from stdin
cat passport.json | go run ./cmd/proofgate evaluate
```

### `export`

Evaluates a passport and exports the sanitized event to Databricks.

```bash
go run ./cmd/proofgate export [flags]
```

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--passport` | `-` (stdin) | Path to passport JSON file |
| `--policy` | (default policy) | Path to custom policy JSON file |
| `--now` | current time | Evaluation time in RFC3339 |
| `--dry-run` | `false` | Print event without sending to Databricks |

**Required environment variables** (except with `--dry-run`):

| Variable | Description |
|---|---|
| `DATABRICKS_HOST` | HTTPS URL of Databricks workspace |
| `DATABRICKS_TOKEN` | Bearer authentication token |
| `DATABRICKS_SQL_WAREHOUSE_ID` | SQL warehouse ID |
| `PROOFGATE_DATABRICKS_CATALOG` | Unity Catalog name |
| `PROOFGATE_DATABRICKS_SCHEMA` | Schema name |

**Output (dry-run):** JSON `ChangeEvent` (sanitized, allowlisted fields only)

**Output (live):** JSON `IngestResult`

```json
{
  "statement_id": "stmt-abc123",
  "state": "SUCCEEDED",
  "event_id": "evt-demo-pass-001",
  "payload_hash": "sha256:..."
}
```

### `default-policy`

Outputs the default policy configuration as JSON.

```bash
go run ./cmd/proofgate default-policy
```

**Output:** JSON `Policy` object with all thresholds and weights.

### `ci`

CI integration subcommand for use inside GitHub Actions workflows. Builds a Change Passport from Entire checkpoint data and test reports, then evaluates it.

Reference: `proofgate/cmd/proofgate/ci.go`

---

## Risk evaluation codes

### Risk reasons (scored)

| Code | Default Points | Trigger condition |
|---|---|---|
| `MISSING_ENTIRE_CHECKPOINT` | 35 | AI-authored change with no Entire checkpoint |
| `INCOMPLETE_PROVENANCE` | 35 | Authoring trail is incomplete |
| `EXCESS_AGENT_HANDOFFS` | 10 | Handoff count > `MaximumSafeHandoffs` (2) |
| `SENSITIVE_COMPONENT` | 20 | Change impacts a sensitive component |
| `NO_TEST_EVIDENCE` | 25 | No test execution with code changes |
| `REQUIRED_TEST_MISSING` | 25 | Required test suite did not run |
| `REQUIRED_TEST_FAILED` | 50 | At least one test failed |
| `DEEP_DEPENDENCY_REACH` | 10 | Dependency depth > `MaximumSafeDepth` (3) |
| `ABNORMAL_FILE_COUNT` | 15 | Changed files > historical p95 |
| `ABNORMAL_BLAST_RADIUS` | 20 | Impacted entities > historical p95 |
| `SIMILAR_CHANGES_FAILED` | 15 | Similar historical changes failed at elevated rate |
| `COMPONENT_FAILURE_RATE` | 10 | Component has elevated failure rate |
| `REPEATED_FAILURE_PATTERN` | 10 | Repeated failure pattern detected |

### Hard stops (blocking)

| Code | Trigger condition |
|---|---|
| `MISSING_ENTIRE_CHECKPOINT` | AI-authored without checkpoint (also scores points) |
| `POLICY_DENIED_COMPONENT` | Change touches a policy-denied component |
| `REQUIRED_TEST_MISSING` | Required test suite not run (also scores points) |
| `REQUIRED_TEST_FAILED` | Test failure detected (also scores points) |
| `SECRET_DETECTED` | Potential secret material found |
| `CONTENT_EXPORT_WITHOUT_CONSENT` | Raw content export without repository consent |
| `REPOSITORY_NOT_OPTED_IN` | Repository has not opted in to ProofGate export |

### Decision thresholds (default policy)

| Score range | Decision |
|---|---|
| 0–29 | `PASS` |
| 30–59 | `WARN` |
| 60–100 (or any hard stop) | `APPROVAL_REQUIRED` |

---

## External agent protocol

Each agent binary exposes these subcommands via stdin/stdout JSON:

### Identity & detection

| Subcommand | Input | Output | Description |
|---|---|---|---|
| `info` | none | `InfoResponse` | Agent name, capabilities, hooks, protected files |
| `detect` | none | `DetectResponse` | Whether agent environment is present |

### Session management

| Subcommand | Flags | Input | Output |
|---|---|---|---|
| `get-session-id` | none | `HookInputJSON` (stdin) | `SessionIDResponse` |
| `get-session-dir` | `--repo-path` | none | `SessionDirResponse` |
| `resolve-session-file` | `--session-dir`, `--session-id` | none | `SessionFileResponse` |
| `read-session` | none | `HookInputJSON` (stdin) | `AgentSessionJSON` |
| `write-session` | none | `AgentSessionJSON` (stdin) | (none) |

### Transcript analysis

| Subcommand | Flags | Input | Output |
|---|---|---|---|
| `get-transcript-position` | `--path` | none | `TranscriptPositionResponse` |
| `extract-modified-files` | `--path`, `--offset` | none | `ExtractFilesResponse` |
| `extract-prompts` | `--session-ref`, `--offset` | none | `ExtractPromptsResponse` |
| `extract-summary` | `--session-ref` | none | `ExtractSummaryResponse` |
| `calculate-tokens` | `--offset` | raw bytes (stdin) | `TokenUsageResponse` |
| `compact-transcript` | `--session-ref` | none | `CompactTranscriptResponse` |

### Transcript I/O

| Subcommand | Flags | Input | Output |
|---|---|---|---|
| `read-transcript` | `--session-ref` | none | raw bytes |
| `chunk-transcript` | `--max-size` | raw bytes (stdin) | `ChunkResponse` |
| `reassemble-transcript` | none | `ChunkResponse` (stdin) | raw bytes |
| `prepare-transcript` | `--session-ref` | none | (agent-specific) |

### Hook management

| Subcommand | Flags | Input | Output |
|---|---|---|---|
| `parse-hook` | `--hook` | `HookInputJSON` (stdin) | `EventJSON` |
| `install-hooks` | `--local-dev`, `--force` | none | `HooksInstalledCountResponse` |
| `uninstall-hooks` | none | none | `HooksInstalledCountResponse` |
| `are-hooks-installed` | none | none | `AreHooksInstalledResponse` |
| `format-resume-command` | `--session-id` | none | `ResumeCommandResponse` |

### GitHub Actions custom commands

| Subcommand | Flags | Description |
|---|---|---|
| `capture-start` | `--session-id`, `--prompt` | Begin capture session (dispatches run-start + turn-start) |
| `capture-finish` | `--execution-file`, `--session-id` | End capture (stores transcript, dispatches turn-end + run-end) |

---

## Control Room API

The Control Room (`proofgate/app/app.py`) exposes REST endpoints for the review UI.

Reference: `proofgate/app/app.py`, `proofgate/app/store.py`

**TODO: Needs verification** — The Control Room API endpoints should be verified against the running application as they depend on Flask route definitions that may vary by deployment configuration.

---

## Change Passport schema

The primary input to ProofGate evaluation. Full type definitions in `proofgate/contracts/passport.go`.

```json
{
  "schema_version": "1.0",
  "event_id": "evt-demo-pass-001",
  "occurred_at": "2026-09-04T10:30:00Z",
  "repository": {
    "id": "org/repo",
    "opted_in": true,
    "is_public": false
  },
  "change": {
    "commit_sha": "abc123",
    "checkpoint_id": "cp-001",
    "intent_summary": "Add input validation",
    "ai_authored": true
  },
  "authoring": {
    "source_adapter": "github-actions@1",
    "agent_family": "claude-code-action",
    "model_family": "sonnet",
    "session_count": 1,
    "handoff_count": 0,
    "tool_categories": ["read", "edit", "test"],
    "provenance_complete": true
  },
  "impact": {
    "changed_files": ["src/validate.go"],
    "changed_line_count": 45,
    "impacted_entities": ["Validate", "ParseInput"],
    "dependency_depth": 1,
    "sensitive_components": [],
    "denied_components": []
  },
  "tests": {
    "total": 18,
    "failed": 0,
    "required_test_missing": false,
    "required_suites": ["unit"],
    "passed_suites": ["unit"]
  },
  "history": {
    "available": true,
    "snapshot_at": "2026-09-04T06:00:00Z",
    "baseline_window_days": 30,
    "baseline_change_count": 28
  },
  "safety": {
    "secret_detected": false,
    "redaction_version": "proofgate-allowlist-v1",
    "dropped_field_count": 0,
    "raw_content_exported": false
  },
  "evidence_ids": ["cp-001"]
}
```

---

## Token usage response

```json
{
  "input_tokens": 8421,
  "cache_creation_tokens": 0,
  "cache_read_tokens": 0,
  "output_tokens": 2194,
  "api_call_count": 1
}
```

The token calculator handles multiple field naming conventions across different transcript formats:
- `input_tokens` / `inputTokens`
- `output_tokens` / `outputTokens`
- `cache_creation_input_tokens` / `cacheCreationInputTokens` / `cache_creation_tokens`
- `cache_read_input_tokens` / `cacheReadInputTokens` / `cache_read_tokens`
