# ProofGate Control Room

This is the human-review surface for ProofGate. It is functional, not a static
dashboard: reviewers inspect a Change Passport, approve or reject it with a
reason, and create an immutable, versioned audit event.

The local slice uses SQLite and four clearly labelled synthetic changes so the
event demo remains reliable without Wi-Fi. It enforces the same invariants as
the planned Lakebase deployment:

- the automated policy verdict is never rewritten by a human decision;
- every review includes an actor and reason;
- `expected_version` prevents a stale browser from overwriting a newer review;
- a unique `idempotency_key` makes network retries safe;
- the review queue and metrics are derived from persisted state.

## Run locally

Python 3.11 or later is sufficient; no packages are required.

```bash
cd proofgate/app
python3 app.py
```

Open <http://127.0.0.1:8000>. Local state is written to
`proofgate/app/.local/proofgate.db` and is intentionally ignored by git.

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
| `GET` | `/api/metrics` | Metrics derived from persisted gates |
| `POST` | `/api/demo/reset` | Restore labelled synthetic demo state |

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

