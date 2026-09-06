# ProofGate Databricks Phase 2

Phase 2 turns the Phase 1 evidence tables into an operational product. One
Declarative Automation Bundle manages:

```text
Entire checkpoint metadata
          │
          ▼
  allowlisted Delta tables ──► Lakeflow incremental signals
          │                    │                 │
          ├──► AI Search       ├──► Fleet AI/BI └──► Ask ProofGate
          │    similarity      │    dashboard         (Genie)
          │                    │
          └────────────────────▼
                    ProofGate Control Room
                 │         │             │
         Lakebase state   AI explain   GitHub re-run
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
  the explanation endpoint, Genie, and a bundle-managed secret scope.
- **AI/BI Fleet Control** exposes review pressure, policy outcomes, Graph
  coverage, and block reasons; it is an operational view, not a generic vanity
  dashboard.
- **Ask ProofGate (Genie)** answers bounded fleet questions from two governed
  tables. Its instructions explicitly forbid claims that Genie can change a
  deterministic verdict.
- **GitHub dispatch** automatically re-evaluates the exact blocked commit after
  an approval receipt is successfully published to Delta.

## Prerequisites

1. Deploy and run Phase 1 from `proofgate/databricks` so the Delta source tables
   exist and `similar_change_documents` has Change Data Feed enabled.
2. Authenticate the CLI to a workspace with Unity Catalog, serverless SQL,
   Lakebase Autoscaling, Databricks Apps, AI Search, and Foundation Model APIs.
3. Copy the ID of the workspace's existing SQL warehouse.
4. Confirm the two model endpoint defaults are available in the workspace's
   region. Override either variable if the workspace exposes another endpoint.
5. Create a fine-grained GitHub token for the demo repository with
   **Actions: write** and **Contents: read**. The deployment helper sends it
   directly to the Databricks secret prompt; it is never written to this repo.

## Validate and deploy

```bash
cd proofgate/databricks/phase2
databricks bundle validate -t dev --var warehouse_id=<warehouse-id>
databricks bundle deploy -t dev --var warehouse_id=<warehouse-id>
databricks bundle run -t dev live_features --var warehouse_id=<warehouse-id>
```

The preferred event path is one repeatable helper from `proofgate/databricks`:

```bash
export PROOFGATE_DATABRICKS_PROFILE=proofgate
export PROOFGATE_WAREHOUSE_ID=<warehouse-id>
export PROOFGATE_GITHUB_REPOSITORY=<owner/repository>
./deploy-demo.sh all
```

If the workspace region exposes different pay-per-token endpoints, also set
`PROOFGATE_EMBEDDING_MODEL_ENDPOINT` and
`PROOFGATE_EXPLANATION_MODEL_ENDPOINT` before running the helper.

It deploys Phase 1, creates the scoped GitHub secret resource, prompts for the
token without placing it in a command-line argument, deploys Phase 2, refreshes
both pipelines, triggers AI Search synchronization, smoke-tests search and the
explanation model, and prints links to the App/dashboard/Genie resources.

The `demo` target runs the Phase 1 transformation every five minutes and the
operational feature refresh two minutes later. AI Search remains deliberately
`TRIGGERED`; `deploy-demo.sh verify` provides the reliable explicit sync used
for the demo rather than hiding extra continuous compute.

The project deliberately uses `0.5–1.0` Lakebase compute units with a 300-second
idle suspension and a triggered (not continuous) AI Search sync to remain
reasonable for a hackathon account. Verify workspace quotas before deployment.

## Honest demo mode

The deployed App uses live Lakebase state by default. For the event demo, add
`PROOFGATE_SEED_DEMO=1` to the App configuration only if no real checkpoint
events have been imported. The UI visibly labels this as synthetic demo data.

## Honest readiness boundary

Everything in this directory can be parsed and tested without cloud secrets,
but a Databricks workspace is the authority for resource availability and
quotas. We do not label the dashboard, Genie answers, model response, Lakebase
connection, or AI Search result as verified until `./deploy-demo.sh verify`
succeeds against the actual event workspace.

Raw prompt/code ingestion stays intentionally disabled. Adding it would broaden
the privacy and secret-scanning surface without improving the core judging
story; Graph-derived entities, counts, reason codes, and safe summaries already
provide the evidence required for ProofGate.
