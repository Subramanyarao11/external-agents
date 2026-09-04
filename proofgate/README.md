# ProofGate engine

ProofGate turns an Entire-backed Change Passport into a reproducible engineering
decision. The engine is deterministic: historical facts from Databricks may
raise risk, but generated text can never change the verdict.

## Run

```bash
go run ./cmd/proofgate evaluate \
  --passport examples/pass.json \
  --now 2026-09-04T12:00:00Z

go run ./cmd/proofgate evaluate \
  --passport examples/approval-required.json \
  --now 2026-09-04T12:00:00Z
```

Build a passport directly from a real git commit, its `Entire-Checkpoint`
trailer, Entire's checkpoint explanation, the git diff, and structured CI test
evidence:

```bash
go run ./cmd/proofgate junit-report \
  --junit proofgate=examples/junit.xml \
  --evidence-prefix github-run-123-attempt-1 \
  --output examples/generated-test-report.json

go run ./cmd/proofgate gate \
  --repo /path/to/target-repository \
  --repo-id mobile-checkout \
  --repo-opted-in \
  --tests examples/test-report.json
```

`junit-report` accepts repeated `--junit name=path` inputs and repeated
`--optional-junit name=path` inputs. It records only suite names, counts,
pass/fail/skip state and an opaque run ID—never test output, stack traces,
source snippets or secrets. A fully skipped required suite becomes missing test
evidence and triggers the appropriate hard stop.

`gate` emits both the generated passport and its deterministic result.
`build-passport` emits only the passport when separate evaluation/export steps
are useful. An AI-authored commit without an Entire trailer stays marked as
AI-authored and therefore triggers the provenance hard stop; it is never
silently treated as human-authored.

For CI, use the composite action in `.github/actions/proofgate-evaluate`. It
publishes a compact GitHub job summary, exposes decision/score/checkpoint
outputs, and writes the full structured result for artifact retention:

```yaml
- id: proofgate
  uses: ./.github/actions/proofgate-evaluate
  with:
    repository-id: mobile-checkout
    repository-opted-in: "true"
    test-report: proofgate-test-report.json
    fail-on: approval-required
    databricks-mode: roundtrip
```

`proofgate/examples/github/proofgate.yml` is a complete workflow template. The
default threshold fails only `APPROVAL_REQUIRED`; choose `warn` for a stricter
gate or `never` for observation mode. The full JSON is still produced before a
blocking exit so an `if: always()` artifact step can preserve the evidence.

## AI-authored pull requests in GitHub Actions

ProofGate includes two dispatchable authoring workflows:

- `examples/github/ai-author-codex.yml` uses the official OpenAI Codex Action,
  keeps its rollout under `RUNNER_TEMP`, and needs `OPENAI_API_KEY`.
- `examples/github/ai-author-cursor.yml` uses Cursor Agent CLI `stream-json`,
  redirects the stream under `RUNNER_TEMP`, and needs `CURSOR_API_KEY`.

Copy them to `.github/workflows/ai-author-codex.yml` and
`.github/workflows/ai-author-cursor.yml`. Copy the evaluator template to the
exact path `.github/workflows/proofgate.yml`, because both authoring workflows
explicitly dispatch that workflow after opening their pull request. This avoids
GitHub's recursion protection, under which a pull request created with the
repository `GITHUB_TOKEN` does not itself trigger another workflow.

Both providers use the checked-in `.github/actions/entire-proofgate` capture
boundary. The capture action normalizes Claude JSON, Codex execution/rollout
JSONL, and Cursor stream JSONL into one Entire session format. The later commit
receives an `Entire-Checkpoint` trailer, the checkpoint ref is pushed, and the
normal ProofGate workflow evaluates the branch. Raw provider transcripts remain
runner-local and are never uploaded by these templates.

A regular GitHub Free account is sufficient. Use a public repository for free
standard hosted-runner usage, or stay within the private-repository Actions
allowance. No GitHub Enterprise feature or larger runner is required.

Before the first run, enable Actions in the demo repository and allow workflow
tokens to write repository contents and pull requests. Add `OPENAI_API_KEY`
for the Codex workflow and/or `CURSOR_API_KEY` for the Cursor workflow as
repository Actions secrets. These are provider credentials; a GitHub or Codex
app subscription does not replace them. Use synthetic code in a public demo
repository, because the resulting Entire checkpoint is intentionally committed
as provenance even though the raw runner transcript is not uploaded.

For a small, tightly scoped edit, budget roughly 5–10 minutes from workflow
dispatch to a ProofGate result. This is a demo target, not a guarantee: hosted
runner queueing and model latency vary, and a cold Databricks SQL warehouse can
add several minutes. Keep `databricks-mode: off` while proving the core loop,
then warm the warehouse and switch to `roundtrip` for the Databricks judging
demo. Both authoring templates have a 20-minute safety timeout.

`databricks-mode: roundtrip` first loads the previous 30 days of allowlisted
fleet evidence, then evaluates the deterministic policy, and finally exports
the current result. `history` and `export` run one half of that loop; `off`
keeps the gate local. Databricks outages degrade explicitly to local evidence
by default. Set `databricks-required: "true"` if your production policy should
fail closed when that evidence is unavailable.

On a blocked change, the Control Room writes an immutable approve/reject receipt
to `policy_decision_events`. Re-running the action reads that receipt by the
passport event ID: approval produces `HUMAN_APPROVED` and releases the check;
rejection produces `HUMAN_REJECTED` and blocks even in observation mode. The
automated verdict remains unchanged in the artifact for auditability.

The Databricks modes read these GitHub secrets/environment variables:

```text
DATABRICKS_HOST
DATABRICKS_TOKEN
DATABRICKS_SQL_WAREHOUSE_ID
PROOFGATE_DATABRICKS_CATALOG
PROOFGATE_DATABRICKS_SCHEMA
```

Decisions are `PASS`, `WARN`, or `APPROVAL_REQUIRED`. Hard stops always require
review, even when their numeric score alone would be lower than the threshold.

## Privacy boundary

The engine accepts structured evidence IDs, counts, categories and safe intent
summaries. It does not require raw prompts, chain-of-thought, source code, URLs
with credentials, or secret values. The Databricks exporter will enforce the
same allowlist before network transmission.
