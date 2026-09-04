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
go run ./cmd/proofgate gate \
  --repo /path/to/target-repository \
  --repo-id mobile-checkout \
  --repo-opted-in \
  --tests examples/test-report.json
```

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
```

`proofgate/examples/github/proofgate.yml` is a complete workflow template. The
default threshold fails only `APPROVAL_REQUIRED`; choose `warn` for a stricter
gate or `never` for observation mode. The full JSON is still produced before a
blocking exit so an `if: always()` artifact step can preserve the evidence.

Decisions are `PASS`, `WARN`, or `APPROVAL_REQUIRED`. Hard stops always require
review, even when their numeric score alone would be lower than the threshold.

## Privacy boundary

The engine accepts structured evidence IDs, counts, categories and safe intent
summaries. It does not require raw prompts, chain-of-thought, source code, URLs
with credentials, or secret values. The Databricks exporter will enforce the
same allowlist before network transmission.
