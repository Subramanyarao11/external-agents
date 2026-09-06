# ProofGate Control Room

This is the human-review surface for ProofGate. It is functional, not a static
dashboard: reviewers inspect a Change Passport, approve or reject it with a
reason, and create an immutable, versioned audit event.

**Live deployment:** [ProofGate Control Room on Databricks Apps](https://proofgate-control-room-7474650058385243.aws.databricksapps.com/)

The local slice uses SQLite and four clearly labelled synthetic changes so the
event demo remains reliable without Wi-Fi. It enforces the same invariants as
the planned Lakebase deployment:

- the automated policy verdict is never rewritten by a human decision;
- every review includes an actor and reason;
- `expected_version` prevents a stale browser from overwriting a newer review;
- a unique `idempotency_key` makes network retries safe;
- a successful live review is idempotently merged into Databricks so the next
  CI run can enforce `HUMAN_APPROVED` or `HUMAN_REJECTED`;
- after a governed approval is synced, an optional GitHub dispatcher re-runs
  the evaluator against the exact blocked commit SHA;
- the review queue and metrics are derived from persisted state.

## Run locally

Python 3.11 or later is sufficient; no packages are required.

```bash
cd proofgate/app
python3 app.py
```

Open <http://127.0.0.1:8000>. Local state is written to
`proofgate/app/.local/proofgate.db` and is intentionally ignored by git.

## Run in Databricks Apps

The checked-in `app.yaml` selects Lakebase mode. Attach a Lakebase Autoscaling
database to the App with the resource key `proofgate-postgres`. Databricks
injects the `PG*` connection values and resolves that resource key to the
endpoint path. The store uses the App service principal to mint a fresh OAuth
database credential whenever the connection pool opens a connection; no static
database password is stored.

Set `PROOFGATE_SEED_DEMO=1` only for a labelled event demonstration. Without it,
the deployed app starts with an empty live queue and `/api/demo/reset` is
disabled.

The same attached SQL Warehouse is used to publish allowlisted human decision
receipts to `policy_decision_events`. The write uses bound parameters and the
review idempotency key. If the warehouse is temporarily unavailable, the API
returns `decision_sync.status=PENDING_RETRY` rather than claiming CI was
released; retrying the same decision request is safe.

The bundle injects `PROOFGATE_GITHUB_REPOSITORY`, workflow name/ref, and a
Databricks secret-backed `PROOFGATE_GITHUB_TOKEN`. Rejections are never
dispatched. Approval dispatch failure is reported as `PENDING_RETRY`; it never
rewrites a successfully persisted review or falsely claims that CI was released.

## Verify

```bash
cd proofgate/app
PROOFGATE_QUIET=1 python3 -m unittest -v
```

## API

| Method | Route | Purpose |
| --- | --- | --- |
| `GET` | `/healthz` | Deployment health and active data mode |
| `GET` | `/api/gates` | Review queue |
| `GET` | `/api/gates/{event_id}` | Evidence and review history |
| `POST` | `/api/gates/{event_id}/decision` | Versioned approve/reject action |
| `POST` | `/api/gates/{event_id}/explain` | Advisory explanation from allowlisted evidence |
| `POST` | `/api/gates/{event_id}/similar` | Historical matches from categorical evidence |
| `GET` | `/api/metrics` | Metrics derived from persisted gates |
| `POST` | `/api/demo/reset` | Restore labelled synthetic demo state |
| `POST` | `/api/admin/sync` | Idempotently import governed gold evidence |

Example decision body:

```json
{
  "action": "APPROVE",
  "actor_id": "demo.evaluator",
  "reason": "Required evidence was reviewed and the failure was reproduced as flaky.",
  "expected_version": 1,
  "idempotency_key": "a-new-unique-value"
}
```
