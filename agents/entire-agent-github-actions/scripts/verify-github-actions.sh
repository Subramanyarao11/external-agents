#!/usr/bin/env bash
set -euo pipefail

AGENT_NAME="GitHub Actions AI Workflow"
AGENT_SLUG="github-actions"
PROBE_DIR=""
EXECUTION_FILE=""
EVENT_FILE=""
NORMALIZED_EXECUTION_FILE=""
RUN_COMMAND=""
USE_SAMPLE=0
KEEP=0

usage() {
  printf '%s\n' \
    "Usage: $0 [--execution-file PATH --event-file PATH] [--run-cmd CMD] [--sample] [--keep]" \
    "" \
    "Use --execution-file with the execution_file output from anthropics/claude-code-action@v1." \
    "Use --event-file with GITHUB_EVENT_PATH from the same run." \
    "--run-cmd executes a command first, then reads RUNNER_TEMP/claude-execution-output.json." \
    "--sample validates a source-derived fixture only; it is not live-runner verification."
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --execution-file) EXECUTION_FILE=${2:?missing path}; shift 2 ;;
    --event-file) EVENT_FILE=${2:?missing path}; shift 2 ;;
    --run-cmd) RUN_COMMAND=${2:?missing command}; shift 2 ;;
    --sample) USE_SAMPLE=1; shift ;;
    --keep) KEEP=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) printf 'Unknown argument: %s\n' "$1" >&2; usage >&2; exit 2 ;;
  esac
done

command -v jq >/dev/null 2>&1 || { printf 'FAIL: jq is required\n' >&2; exit 1; }

if [[ -n "$RUN_COMMAND" ]]; then
  bash -lc "$RUN_COMMAND"
  EXECUTION_FILE=${EXECUTION_FILE:-${RUNNER_TEMP:-}/claude-execution-output.json}
fi

PROBE_DIR="$(mktemp -d "${TMPDIR:-/tmp}/entire-gha-probe.XXXXXX")"
mkdir -p "$PROBE_DIR/captures"

cleanup() {
  if [[ $KEEP -eq 1 ]]; then
    printf 'Kept captures: %s\n' "$PROBE_DIR"
  else
    rm -rf "$PROBE_DIR"
  fi
}
trap cleanup EXIT

if [[ $USE_SAMPLE -eq 1 ]]; then
  EXECUTION_FILE="$PROBE_DIR/claude-execution-output.json"
  EVENT_FILE="$PROBE_DIR/event.json"
  printf '%s\n' '[
    {"type":"system","subtype":"init","session_id":"sample-session-123","model":"claude-sonnet"},
    {"type":"user","message":{"role":"user","content":[{"type":"text","text":"Create proof.txt"}]}},
    {"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Creating the file."},{"type":"tool_use","id":"tool-1","name":"Write","input":{"file_path":"proof.txt","content":"verified"}}]}},
    {"type":"result","subtype":"success","is_error":false,"result":"Created proof.txt","num_turns":1,"total_cost_usd":0.01,"usage":{"input_tokens":120,"output_tokens":35}}
  ]' > "$EXECUTION_FILE"
  printf '%s\n' '{"action":"created","issue":{"number":348},"comment":{"id":123,"body":"@claude create proof.txt"},"repository":{"id":42,"full_name":"entireio/example"},"sender":{"login":"sample-user"}}' > "$EVENT_FILE"
  printf 'WARN: using source-derived sample; no live GitHub runner was verified\n'
fi

[[ -f "$EXECUTION_FILE" ]] || { printf 'FAIL: execution file not found: %s\n' "$EXECUTION_FILE" >&2; exit 1; }
[[ -f "$EVENT_FILE" ]] || { printf 'FAIL: event file not found: %s\n' "$EVENT_FILE" >&2; exit 1; }
jq -e 'type == "object"' "$EVENT_FILE" >/dev/null
NORMALIZED_EXECUTION_FILE="$PROBE_DIR/execution-records.json"
jq -s '
  if length == 1 and (.[0] | type) == "array" then .[0]
  elif length > 0 and all(.[]; type == "object") then .
  else error("execution file must be a JSON array or JSONL object stream")
  end
' "$EXECUTION_FILE" > "$NORMALIZED_EXECUTION_FILE"

SESSION_ID=$(jq -r '[.[] | select((.type == "system" and .subtype == "init") or .event == "session_started") | .session_id // empty][0] // empty' "$NORMALIZED_EXECUTION_FILE")
[[ -n "$SESSION_ID" ]] || SESSION_ID="gha-${GITHUB_RUN_ID:-local}-${GITHUB_RUN_ATTEMPT:-1}-${GITHUB_JOB:-probe}"
MODEL=$(jq -r '[.[] | .model // empty][0] // empty' "$NORMALIZED_EXECUTION_FILE")
PROMPT=$(jq -r '[.[] | if .event == "user_prompt" then .text else (.message.content[]? | select(.type == "text") | .text) end][0] // empty' "$NORMALIZED_EXECUTION_FILE")
TIMESTAMP=$(date -u +%Y-%m-%dT%H:%M:%SZ)

write_event() {
  local filename=$1
  local event_type=$2
  local prompt=${3:-}
  jq -n \
    --argjson type "$event_type" \
    --arg session_id "$SESSION_ID" \
    --arg session_ref "$EXECUTION_FILE" \
    --arg prompt "$prompt" \
    --arg model "$MODEL" \
    --arg timestamp "$TIMESTAMP" \
    --arg run_id "${GITHUB_RUN_ID:-local}" \
    --arg run_attempt "${GITHUB_RUN_ATTEMPT:-1}" \
    --arg job "${GITHUB_JOB:-probe}" \
    '{type:$type,session_id:$session_id,session_ref:$session_ref,model:$model,timestamp:$timestamp,metadata:{github_run_id:$run_id,github_run_attempt:$run_attempt,github_job:$job}} + (if $prompt == "" then {} else {prompt:$prompt} end)' \
    > "$PROBE_DIR/captures/$filename.json"
}

write_event session-start 1
write_event turn-start 2 "$PROMPT"
write_event turn-end 3
write_event session-end 5

printf 'Agent: %s (%s)\n' "$AGENT_NAME" "$AGENT_SLUG"
printf 'Runner environment: %s\n' "${GITHUB_ACTIONS:-false}"
printf 'Session ID: %s\n' "$SESSION_ID"
printf 'Execution messages: %s\n' "$(jq 'length' "$NORMALIZED_EXECUTION_FILE")"
printf 'User messages: %s\n' "$(jq '[.[] | select(.type == "user" or .event == "user_prompt")] | length' "$NORMALIZED_EXECUTION_FILE")"
printf 'Assistant messages: %s\n' "$(jq '[.[] | select(.type == "assistant" or .event == "agent_response")] | length' "$NORMALIZED_EXECUTION_FILE")"
printf 'Tool uses: %s\n' "$(jq '[.[] | select(.event == "tool_call"), (.message.content[]? | select(.type == "tool_use"))] | length' "$NORMALIZED_EXECUTION_FILE")"
printf 'Result messages: %s\n' "$(jq '[.[] | select(.type == "result" or .event == "checkpoint_created")] | length' "$NORMALIZED_EXECUTION_FILE")"

for capture in "$PROBE_DIR"/captures/*.json; do
  printf '\n--- %s ---\n' "$(basename "$capture")"
  jq . "$capture"
done

if [[ "${GITHUB_ACTIONS:-false}" == "true" && $USE_SAMPLE -eq 0 ]]; then
  printf '\nPASS: live GitHub Actions payload structure verified\n'
else
  printf '\nWARN: structure verified locally; run again inside GitHub Actions for a live verdict\n'
fi
