# ProofGate Databricks Phase 2

Phase 2 turns the Phase 1 evidence tables into an operational product. One
Declarative Automation Bundle manages:

```text
Entire checkpoint metadata
          │
          ▼
  allowlisted Delta tables ──► Lakeflow incremental signals
          │                              │
          └──► AI Search similarity      └──► fleet metrics
                         │
                         ▼
                 ProofGate Control Room
                  │         │          │
          Lakebase state   AI explain  SQL evidence
```

- **Lakebase** stores low-latency mutable review state and immutable decision
  events. It scales down after five idle minutes.
- **Lakeflow** incrementally reads only the allowlisted silver event contract;
  it never ingests prompts or source code.
- **AI Search** embeds the categorical `similarity_summary`, allowing a reviewer
  to retrieve earlier changes with similar risk/test/provenance patterns.
- **Foundation Model API** generates a short plain-language explanation. Its
  output is advisory and can never modify the deterministic ProofGate verdict.
- **Databricks Apps** hosts the Control Room with a dedicated service principal
  and least-privilege bindings to Lakebase, the SQL warehouse, the search index,
  and the explanation endpoint.

## Prerequisites

1. Deploy and run Phase 1 from `proofgate/databricks` so the Delta source tables
   exist and `similar_change_documents` has Change Data Feed enabled.
2. Authenticate the CLI to a workspace with Unity Catalog, serverless SQL,
   Lakebase Autoscaling, Databricks Apps, AI Search, and Foundation Model APIs.
3. Copy the ID of the workspace's existing SQL warehouse.
4. Confirm the two model endpoint defaults are available in the workspace's
   region. Override either variable if the workspace exposes another endpoint.

## Validate and deploy

```bash
cd proofgate/databricks/phase2
databricks bundle validate -t dev --var warehouse_id=<warehouse-id>
databricks bundle deploy -t dev --var warehouse_id=<warehouse-id>
databricks bundle run -t dev live_features --var warehouse_id=<warehouse-id>
```

The project deliberately uses `0.5–1.0` Lakebase compute units with a 300-second
idle suspension and a triggered (not continuous) AI Search sync to remain
reasonable for a hackathon account. Verify workspace quotas before deployment.

## Honest demo mode

The deployed App uses live Lakebase state by default. For the event demo, add
`PROOFGATE_SEED_DEMO=1` to the App configuration only if no real checkpoint
events have been imported. The UI visibly labels this as synthetic demo data.

