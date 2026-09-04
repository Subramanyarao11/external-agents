# ProofGate judging readiness

This file separates code-complete work from checks that can only be truthful
after connecting the event Databricks workspace and demo GitHub repository.

## Code complete and locally verified

| Capability | Implementation | Local evidence |
| --- | --- | --- |
| Entire Graph impact | Pinned Graph v0.4.0 contract, symbol-level entities, dependent fan-out risk, explicit partial/fallback labels | Go tests plus live local `entire graph commit --json` contract check |
| GitHub evaluator | Exact-SHA `workflow_dispatch`, Graph required, per-commit concurrency | `actionlint` |
| Human release loop | Delta decision receipt first, then approval-only GitHub dispatch; rejection never dispatches | 13 Control Room tests with fake SQL/GitHub clients |
| Scheduled medallion data | Five-minute Bronze → Silver → Gold job in the `demo` target | Bundle schema check |
| Operational features | Lakeflow refresh at minute 2 of each five-minute window | Bundle schema check |
| AI/BI Fleet Control | Four governed datasets and eight dashboard widgets | JSON contract check |
| Ask ProofGate | Three sample questions, three verified query patterns, bounded instructions | Inline Genie v2 bundle contract |
| AI Search/model checks | Repeatable sync, query, and model smoke-test commands | CLI argument/shell syntax checks |

Run all local evidence checks with:

```bash
./scripts/verify-offline.sh
```

## Credentials and live checks still required

1. Authenticate a Databricks CLI profile and set the warehouse ID.
2. Set the actual `owner/repository`; the active evaluator is already checked in
   at `.github/workflows/proofgate.yml`.
3. Run `./databricks/deploy-demo.sh all`; enter the fine-grained GitHub token at
   the Databricks secret prompt.
4. Open the printed App, Fleet dashboard, and Ask ProofGate links.
5. Execute the judging path once: blocked PR → Control Room approval → new
   evaluator run on the same SHA → `HUMAN_APPROVED`.
6. Record the successful workflow URL and keep one pre-seeded offline demo as a
   fallback for venue connectivity.

These are live validations, not remaining product implementation. Workspace
feature availability, quotas, App egress, and endpoint names cannot be proven
without the real workspace.

## Deliberately deferred

- Raw prompt/code ingestion remains disabled. It adds privacy and secret risk
  while weakening ProofGate's bounded-evidence story.
- The curveball adapter is intentionally chosen only after organizers reveal
  the constraint.

Everything else in the current expanded plan is now represented in executable
code, declarative resources, or a repeatable operator command.
