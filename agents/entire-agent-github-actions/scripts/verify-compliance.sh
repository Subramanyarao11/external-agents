#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
AGENT_DIR=$(cd -- "$SCRIPT_DIR/.." && pwd)
REPO_ROOT=$(cd -- "$AGENT_DIR/../.." && pwd)
BINARY=${1:-$AGENT_DIR/entire-agent-github-actions}
TESTS_BINARY=${EXTERNAL_AGENTS_TESTS_BIN:-external-agents-tests}
FIXTURE_FILE="$AGENT_DIR/testdata/claude-action-execution.json"

if [[ ! -x "$BINARY" ]]; then
  printf 'Binary is missing or not executable: %s\n' "$BINARY" >&2
  exit 1
fi
if ! command -v "$TESTS_BINARY" >/dev/null 2>&1; then
  printf 'Compliance runner is not on PATH: %s\n' "$TESTS_BINARY" >&2
  exit 1
fi

TEMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/entire-gha-compliance.XXXXXX")
cleanup() {
  rm -rf -- "$TEMP_DIR"
}
trap cleanup EXIT

jq -n \
  --arg session_ref "$FIXTURE_FILE" \
  '{
    transcript_analyzer: {
      session_ref: $session_ref,
      offset: 0,
      position: 6,
      modified_files: ["proofgate/evaluator.go", "proofgate/evaluator_test.go"],
      prompts: ["Add the risk evaluator", "Add a regression test"],
      summary: "Implemented the risk evaluator and regression coverage.",
      has_summary: true
    }
  }' > "$TEMP_DIR/compliance.json"

cd "$REPO_ROOT"
"$TESTS_BINARY" verify "$BINARY" --fixtures "$TEMP_DIR/compliance.json"
