#!/usr/bin/env bash
set -euo pipefail

agent_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
binary=${1:-$agent_dir/entire-agent-github-actions}

command -v jq >/dev/null 2>&1 || {
  printf 'FAIL: jq is required\n' >&2
  exit 1
}

if [[ ! -x "$binary" ]]; then
  (cd "$agent_dir" && go build -o entire-agent-github-actions ./cmd/entire-agent-github-actions)
fi

verify_fixture() {
  local provider=$1
  local fixture=$2
  local expected_file=$3
  local expected_summary=$4

  local position files summary usage
  position=$("$binary" get-transcript-position --path "$fixture")
  files=$("$binary" extract-modified-files --path "$fixture" --offset 0)
  summary=$("$binary" extract-summary --session-ref "$fixture")
  usage=$("$binary" calculate-tokens --offset 0 < "$fixture")

  jq -e '.position > 0' <<< "$position" >/dev/null
  jq -e --arg file "$expected_file" '.files | index($file) != null' <<< "$files" >/dev/null
  jq -e --arg summary "$expected_summary" '.has_summary == true and .summary == $summary' <<< "$summary" >/dev/null
  jq -e '.input_tokens >= 0 and .output_tokens >= 0' <<< "$usage" >/dev/null

  printf 'PASS: %s fixture normalized (%s)\n' "$provider" "$(basename "$fixture")"
}

verify_fixture \
  codex-exec \
  "$agent_dir/testdata/codex-exec.jsonl" \
  proofgate/evaluator.go \
  'Implemented the evaluator and regression coverage.'

verify_fixture \
  codex-rollout \
  "$agent_dir/testdata/codex-rollout.jsonl" \
  proofgate/evaluator.go \
  'Implemented the evaluator and tests.'

verify_fixture \
  cursor \
  "$agent_dir/testdata/cursor-agent-stream.jsonl" \
  proofgate/evaluator.go \
  'Implemented the evaluator and regression coverage.'

printf 'WARN: provider formats are fixture-verified; hosted runner execution still requires provider secrets.\n'
