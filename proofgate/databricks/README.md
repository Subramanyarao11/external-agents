# ProofGate on Databricks

This bundle deploys the Phase 1 evidence lake and deterministic transformation
job. It intentionally references an existing SQL warehouse so it fits Free
Edition's single-warehouse limit.

## Deploy

```bash
databricks auth login --host https://<workspace-url> --profile proofgate
databricks bundle validate -t dev --profile proofgate \
  --var="warehouse_id=<warehouse-id>"
databricks bundle deploy -t dev --profile proofgate \
  --var="warehouse_id=<warehouse-id>"
databricks bundle run proofgate_prepare -t dev --profile proofgate \
  --var="warehouse_id=<warehouse-id>"
```

The GitHub runner exporter needs short-lived OAuth credentials or a scoped
service-principal token through GitHub Secrets, plus:

```text
DATABRICKS_HOST
DATABRICKS_TOKEN
DATABRICKS_SQL_WAREHOUSE_ID
PROOFGATE_DATABRICKS_CATALOG
PROOFGATE_DATABRICKS_SCHEMA
```

No host, token, warehouse ID, customer repository name, raw prompt, or source
path belongs in git. Use `proofgate export --dry-run` to inspect exactly what
would cross the network.

In CI, `databricks-mode: roundtrip` closes the feedback loop: ProofGate queries
the previous 30 days of `gold_change_risk_features` using bound SQL parameters,
evaluates the new change with that historical snapshot, and idempotently merges
the new allowlisted event into bronze. The query response is capped at one row
and 16 KiB. A Databricks outage is surfaced as `DEGRADED`, never silently
reported as a fully historical decision.
