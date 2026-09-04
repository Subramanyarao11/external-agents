# GitHub Actions AI Workflow — External Agent Research

## Verdict: COMPATIBLE WITH EXPLICIT WORKFLOW INSTRUMENTATION

GitHub Actions is a CI environment rather than a conversational coding-agent
binary. It nevertheless exposes stable run identity and event context, while
Anthropic's official `claude-code-action@v1` exposes both the Claude session ID
and the full Agent SDK execution file. A local composite action can place
Entire lifecycle events around that run without modifying Entire CLI itself.

The implementation targets the open Entire CLI use case described in issue
`entireio/cli#348`: retain the triggering issue/comment, agent trace, file
changes and relationship to the resulting branch or pull request.

Research sources:

- https://github.com/entireio/cli/issues/348
- https://docs.github.com/en/actions/reference/workflows-and-actions/variables
- https://github.com/anthropics/claude-code-action/blob/main/action.yml
- https://github.com/anthropics/claude-code-action/blob/main/base-action/src/run-claude-sdk.ts
- https://github.com/anthropics/claude-code-action/blob/main/base-action/src/execution-file.ts

## Static Checks

| Check | Result | Notes |
|---|---|---|
| Runner present | WARN | `GITHUB_ACTIONS=true` only inside a GitHub Actions runner; false on the development Mac |
| Help available | N/A | GitHub Actions is configured by workflow YAML, not a local CLI |
| Version info | PASS | Workflow records action ref, runner OS/arch and adapter version |
| Hook keywords | PARTIAL | Step boundaries provide lifecycle points; there is no native global hook registry |
| Session keywords | PASS | Claude Code Action outputs `session_id` and `execution_file` |
| Config directory | PASS | `.github/workflows/` and generated `.github/actions/entire-proofgate/` |
| Documentation | PASS | Official GitHub and Anthropic sources listed above |

Local verification on 4 September 2026 passed the shared external-agent
compliance suite, semantic Claude execution fixtures, unit tests, and an actual
Entire CLI lifecycle: TurnStart produced a shadow checkpoint, a user commit
received an `Entire-Checkpoint` trailer, and `entire checkpoint explain`
reported the GitHub Actions agent, model, touched file, prompt and tokens. A
hosted runner execution remains unverified until a disposable repository and
Claude credential are available.

## Binary

- External adapter binary: `entire-agent-github-actions`
- Native runtime: GitHub-hosted or self-hosted Actions runner
- Supported AI action initially: `anthropics/claude-code-action@v1`
- GitHub runtime identity: `GITHUB_RUN_ID`, `GITHUB_RUN_ATTEMPT`,
  `GITHUB_JOB`, `GITHUB_WORKFLOW_REF`, `GITHUB_SHA`
- Installation: build the Go binary, place it on `PATH`, enable
  `external_agents` in Entire, and use the generated local composite action in
  the AI workflow.

## Hook Mechanism

The adapter installs a repository-local composite action source under:

```text
.github/actions/entire-proofgate/action.yml
```

The composite action is explicitly referenced twice: `mode: begin` immediately
before Claude and `mode: finish` in an `always()` step immediately after it.
The finish call consumes `steps.<claude-id>.outputs.execution_file`. Both calls
use the same deterministic `gha-<run-id>-<attempt>-<job>` identity, so the
pre-agent snapshot and post-agent evidence belong to one Entire session.

Hook input is JSON on stdin. The workflow never prints the raw transcript.

| Workflow lifecycle | Hook name | Protocol event |
|---|---|---|
| Begin step starts from GitHub event | `run-start` | 1 SessionStart |
| Begin step accepts redacted task intent | `turn-start` | 2 TurnStart |
| Finish step receives execution file | `turn-end` | 3 TurnEnd |
| Finish step finalizes in `always()` | `run-end` | 5 SessionEnd |

There is no compaction mapping. Subagent events are retained inside the Agent
SDK transcript but are not promoted to Entire lifecycle events in v1.

## Session Management

- Session directory on runner:
  `${RUNNER_TEMP}/entire-github-actions/sessions/`
- Local/test fallback:
  `${TMPDIR}/entire-github-actions/<repo-hash>/sessions/`
- Entire session ID: deterministic `gha-<run-id>-<attempt>-<job>`, available
  before Claude starts. The native Claude `session_id` remains transcript
  evidence and can be included in the Change Passport.
- Session file:
  `<session-dir>/<sanitized-session-id>.json`
- Session identity metadata includes the GitHub run URL, repository, event
  name, workflow ref, SHA, actor, triggering actor, PR/issue number, action ref
  and adapter version.

The runner is ephemeral, so the transcript must be checkpointed before the job
ends. Resuming is workflow-specific rather than automatic.

## Transcript

Anthropic's action writes `claude-execution-output.json` under `RUNNER_TEMP`
and exposes its path as `execution_file`. The file is a JSON array of Agent SDK
messages. Official source confirms that messages are collected before log
sanitization and written to the file even when `show_full_output` is false.

Documented message shapes:

```json
{"type":"system","subtype":"init","session_id":"session-123","model":"claude-sonnet"}
{"type":"user","message":{"role":"user","content":[{"type":"text","text":"Fix the failing test"}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"I will inspect the test."},{"type":"tool_use","name":"Edit","input":{"file_path":"src/app.ts"}}]}}
{"type":"result","subtype":"success","is_error":false,"result":"Implemented the fix","num_turns":2,"total_cost_usd":0.01}
```

- User prompt: user messages and the permitted trigger excerpt
- Modified files: `tool_use` blocks for Edit/Write/NotebookEdit plus final
  `git diff --name-status` reconciliation
- Summary: successful result message, falling back to last assistant text
- Tokens: aggregate usage/result fields when present; cost is metadata only
- Model: system init model, falling back to model usage keys

## Data Storage Verification

- Execution file contains actual assistant content: **UNVERIFIED ON A LIVE
  RUNNER**; confirmed by current official action source and fixtures
- Tool calls: current Agent SDK message schema includes them in assistant
  content blocks
- Secondary storage: no persistent storage is assumed; runner temp is copied
  into the Entire checkpoint before teardown
- Cross-reference: Claude `session_id` plus GitHub run ID/attempt/job
- Hook data flow: **UNVERIFIED ON A LIVE RUNNER**; the generated composite
  action will pass explicit JSON via stdin
- Secret safety: raw execution output is never written to Actions logs or a
  Databricks payload

## Protocol Mapping

| Subcommand | Native concept | Implementation notes | Feasibility |
|---|---|---|---|
| `info` | Static metadata | Declares CI hooks and transcript analysis | Required |
| `detect` | Actions runner | Check `GITHUB_ACTIONS=true` and event/workspace variables | Required |
| `get-session-id` | Claude/GitHub identity | Extract from normalized hook | Required |
| `get-session-dir` | Runner temp | Repository-scoped fallback outside working tree | Required |
| `resolve-session-file` | Captured SDK log | Sanitized session ID JSON path | Required |
| `read-session` | Execution file + GitHub event | Preserve native data and file lists | Required |
| `write-session` | Runner session file | Atomic owner-only write | Required |
| `read-transcript` | Execution file | Raw bytes; never log | Required |
| `chunk-transcript` / `reassemble-transcript` | None | Generic byte chunks | Required |
| `compact-transcript` | SDK message array | Convert to Entire Transcript Format | Supported |
| `format-resume-command` | Workflow dispatch/rerun | Emit safe `gh run rerun` fallback | Partial |
| `parse-hook` | Composite action payload | Map the four lifecycle events | Supported |
| `install-hooks` | Local composite action | Create managed action file; no workflow is silently modified | Supported |
| `uninstall-hooks` | Managed action file | Remove only exact adapter-owned file | Supported |
| `are-hooks-installed` | Managed marker/content | Reject foreign files | Supported |
| `get-transcript-position` | SDK message count | Stable message index | Supported |
| `extract-modified-files` | Tool uses + git reconciliation | Allowlisted file-changing tools | Supported |
| `extract-prompts` | User messages | Redaction happens downstream | Supported |
| `extract-summary` | Result/assistant text | Prefer canonical result | Supported |
| `calculate-tokens` | Result usage | Aggregate fields when available | Supported |

## Selected Capabilities

| Capability | Declared | Justification |
|---|---|---|
| `hooks` | true | Explicit, inspectable workflow lifecycle integration |
| `transcript_analyzer` | true | Official action exposes full SDK execution file |
| `transcript_preparer` | false | Execution file already exists after Claude step |
| `compact_transcript` | true | Preserve inspectable prompts, replies and tool calls |
| `token_calculator` | true | Result/usage fields are available when emitted |
| `text_generator` | false | The workflow action, not the adapter, owns model invocation |
| `hook_response_writer` | false | Actions annotations are not an agent response channel |
| `subagent_aware_extractor` | false | Defer until live nested-agent fixtures exist |
| `uses_terminal` | false | Non-interactive CI workflow |

## Gaps and Limitations

- A workflow must explicitly use the generated composite action; GitHub has no
  safe repository-wide lifecycle hook that the adapter can install invisibly.
- The first implementation targets Claude Code Action output. A provider
  interface should permit Codex Action and other agents later.
- `execution_file` is an output path on the same job runner and does not cross
  jobs unless uploaded as an artifact. ProofGate keeps capture in the same job.
- The triggering comment and PR body are untrusted input. They are stored as
  evidence, never executed as workflow instructions.
- Full execution files can contain secrets or source fragments. Databricks
  receives only allowlisted, redacted Change Passport fields.
- `gh run rerun` reproduces the workflow but does not automatically resume the
  Claude session. True resume requires a workflow-dispatch input configured by
  the repository owner.

## Captured Payloads

- Verification script:
  `agents/entire-agent-github-actions/scripts/verify-github-actions.sh`
- Capture directory:
  `agents/entire-agent-github-actions/.probe-github-actions-*/captures/`
- Verification status: **LOCAL LIFECYCLE VERIFIED / LIVE-RUNNER UNVERIFIED**
- The script accepts an actual action `execution_file` and `GITHUB_EVENT_PATH`,
  or can validate a source-derived sample without network or secrets.

## E2E Test Prerequisites

- Entire CLI: `entire` on `PATH` or `E2E_ENTIRE_BIN`
- GitHub Actions: a disposable public/synthetic repository with Actions enabled
- Claude action: `anthropics/claude-code-action@v1`
- Authentication: repository secret or approved OIDC configuration owned by
  the tester; never a fixture
- Non-interactive mode: GitHub workflow `prompt:` input
- Interactive mode: not applicable
- Local contract mode: source-derived execution and event fixtures
- Hosted lifecycle mode: workflow dispatch followed by `gh run watch`
- Timeout multiplier: 4.0 for hosted runner provisioning and model latency
- Transient patterns: runner unavailable, rate limit, overloaded, 429, 503,
  529, workflow cancelled
