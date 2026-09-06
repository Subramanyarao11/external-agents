#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
proofgate_dir="$(cd "$script_dir/.." && pwd)"
repository_dir="$(cd "$proofgate_dir/.." && pwd)"

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Missing required command: $1" >&2
    exit 1
  fi
}

for command_name in go python3 jq git entire databricks; do
  require_command "$command_name"
done

echo "[1/7] Go unit tests"
(cd "$proofgate_dir" && go test ./...)

echo "[2/7] Go static analysis"
(cd "$proofgate_dir" && go vet ./...)

echo "[3/7] Control Room tests"
(cd "$proofgate_dir/app" && PROOFGATE_QUIET=1 python3 -m unittest -v)

echo "[4/7] GitHub workflow lint"
(cd "$repository_dir" && go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.7 \
  .github/workflows/proofgate.yml \
  proofgate/examples/github/proofgate.yml \
  proofgate/examples/github/ai-author-codex.yml \
  proofgate/examples/github/ai-author-cursor.yml)

echo "[5/7] Databricks dashboard asset"
jq -e '
  (.datasets | length) >= 4 and
  (.pages | length) >= 1 and
  ([.pages[].layout[].widget.name] | length) >= 6
' "$proofgate_dir/databricks/phase2/dashboard/proofgate-fleet.lvdash.json" >/dev/null

echo "[6/7] Entire Graph contract"
graph_json="$(entire graph commit HEAD --json --max-seconds 20 --repo "$repository_dir")"
jq -e '
  (.files | type == "array") and
  ((.warnings // []) | type == "array") and
  (all(.files[]?; (.path | type == "string") and (.changes | type == "array")))
' <<<"$graph_json" >/dev/null

validate_bundle() {
  local bundle_dir="$1"
  shift
  local output
  if output="$(cd "$bundle_dir" && databricks bundle validate "$@" 2>&1)"; then
    return 0
  fi
  if grep -q "cannot configure default credentials" <<<"$output"; then
    echo "  Parsed $(basename "$bundle_dir"); live resource validation awaits Databricks authentication."
    return 0
  fi
  printf '%s\n' "$output" >&2
  return 1
}

echo "[7/7] Databricks bundle schemas"
validate_bundle "$proofgate_dir/databricks" -t dev --var warehouse_id=offline-validation
validate_bundle "$proofgate_dir/databricks/phase2" -t dev \
  --var warehouse_id=offline-validation,github_repository=owner/repository

echo "Offline readiness checks passed. Live Databricks and GitHub checks still require credentials."
