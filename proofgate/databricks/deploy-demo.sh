#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
phase2_dir="$script_dir/phase2"

profile="${PROOFGATE_DATABRICKS_PROFILE:-proofgate}"
warehouse_id="${PROOFGATE_WAREHOUSE_ID:-}"
github_repository="${PROOFGATE_GITHUB_REPOSITORY:-}"
target="${PROOFGATE_DATABRICKS_TARGET:-demo}"
catalog="${PROOFGATE_DATABRICKS_CATALOG:-main}"
schema="${PROOFGATE_DATABRICKS_SCHEMA:-proofgate_demo}"
secret_key="${PROOFGATE_GITHUB_SECRET_KEY:-github-token}"
embedding_model="${PROOFGATE_EMBEDDING_MODEL_ENDPOINT:-databricks-qwen3-embedding-0-6b}"
explanation_model="${PROOFGATE_EXPLANATION_MODEL_ENDPOINT:-databricks-gpt-5-6-luna}"
scope="proofgate-github-${target}"
mode="${1:-all}"

if [[ -z "$warehouse_id" || -z "$github_repository" ]]; then
  echo "Set PROOFGATE_WAREHOUSE_ID and PROOFGATE_GITHUB_REPOSITORY=owner/repository." >&2
  exit 1
fi
if [[ ! "$github_repository" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
  echo "PROOFGATE_GITHUB_REPOSITORY must be owner/repository." >&2
  exit 1
fi
if [[ "$mode" != "deploy" && "$mode" != "verify" && "$mode" != "all" ]]; then
  echo "Usage: $0 [deploy|verify|all]" >&2
  exit 1
fi

phase1_vars="warehouse_id=${warehouse_id},catalog=${catalog},schema=${schema}"
phase2_vars="warehouse_id=${warehouse_id},catalog=${catalog},schema=${schema},github_repository=${github_repository},github_token_secret_key=${secret_key},embedding_model_endpoint=${embedding_model},explanation_model_endpoint=${explanation_model}"

deploy() {
  echo "Authenticating with Databricks profile: $profile"
  databricks auth describe -p "$profile" >/dev/null

  echo "Deploying and priming Bronze -> Silver -> Gold"
  (cd "$script_dir" && databricks bundle validate -p "$profile" -t "$target" --var "$phase1_vars")
  (cd "$script_dir" && databricks bundle deploy -p "$profile" -t "$target" --var "$phase1_vars")
  (cd "$script_dir" && databricks bundle run -p "$profile" -t "$target" --var "$phase1_vars" proofgate_prepare)

  echo "Creating the GitHub-token secret scope"
  (cd "$phase2_dir" && databricks bundle deploy -p "$profile" -t "$target" \
    --var "$phase2_vars" --select secret_scopes.github_dispatch)
  echo "Enter the fine-grained GitHub token at the hidden Databricks CLI prompt."
  echo "It needs Actions: write and Contents: read for $github_repository."
  databricks secrets put-secret -p "$profile" "$scope" "$secret_key"

  echo "Deploying Lakebase, App, AI Search, Fleet dashboard, Genie, and scheduled refresh"
  (cd "$phase2_dir" && databricks bundle validate -p "$profile" -t "$target" --var "$phase2_vars")
  (cd "$phase2_dir" && databricks bundle deploy -p "$profile" -t "$target" --var "$phase2_vars")
  (cd "$phase2_dir" && databricks bundle run -p "$profile" -t "$target" \
    --var "$phase2_vars" refresh_operational_features)
}

verify() {
  local index_name="${catalog}.${schema}.similar_change_index"
  echo "Refreshing deterministic feature tables"
  (cd "$script_dir" && databricks bundle run -p "$profile" -t "$target" --var "$phase1_vars" proofgate_prepare)
  (cd "$phase2_dir" && databricks bundle run -p "$profile" -t "$target" \
    --var "$phase2_vars" refresh_operational_features)

  echo "Synchronizing and querying AI Search"
  databricks vector-search-indexes sync-index -p "$profile" "$index_name"
  databricks vector-search-indexes query-index -p "$profile" "$index_name" \
    --query-text "change with incomplete provenance and failing tests" --num-results 3 -o json

  echo "Smoke-testing the advisory explanation model"
  databricks serving-endpoints query -p "$profile" "$explanation_model" \
    --json '{"messages":[{"role":"user","content":"In one sentence, explain why missing provenance should trigger human review."}],"max_tokens":80,"temperature":0}' \
    -o json

  echo "Resource links (open the App, Fleet dashboard, and Ask ProofGate from this summary)"
  (cd "$phase2_dir" && databricks bundle summary -p "$profile" -t "$target" \
    --var "$phase2_vars")
  echo "Final manual assertion: blocked PR -> approve in Control Room -> new evaluator run on the same SHA -> HUMAN_APPROVED."
}

if [[ "$mode" == "deploy" || "$mode" == "all" ]]; then
  deploy
fi
if [[ "$mode" == "verify" || "$mode" == "all" ]]; then
  verify
fi
