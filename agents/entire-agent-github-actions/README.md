# entire-agent-github-actions

External agent binary for capturing AI coding sessions that run inside GitHub
Actions. One provider-neutral lifecycle wraps three execution formats:

| Provider | CI integration | Captured evidence |
|---|---|---|
| Codex | `openai/codex-action@v1` | Private persisted rollout JSONL |
| Cursor | `cursor-agent --output-format stream-json` | Redirected stream JSONL |
| Claude | `anthropics/claude-code-action@v1` | Action `execution_file` JSON |

Codex and Cursor can therefore create real Entire checkpoints in CI without an
Anthropic account. ProofGate consumes the same normalized session contract for
all three providers.

This module is under active development. See [AGENT.md](AGENT.md) for the
protocol mapping and verification status.

## Build

```bash
mise run build
./entire-agent-github-actions info
```

## Protocol compliance

```bash
EXTERNAL_AGENTS_TESTS_BIN="$(command -v external-agents-tests)" \
  ./scripts/verify-compliance.sh
```

## Installation model

1. Put `entire-agent-github-actions` on the runner `PATH`.
2. Enable `external_agents` in `.entire/settings.json`.
3. Run `entire enable --agent github-actions --telemetry=false`.
4. Reference the repository-local action installed at
   `.github/actions/entire-proofgate/action.yml` before and after the AI action.

The checked-in examples are ready to copy into `.github/workflows/`:

- `proofgate/examples/github/ai-author-codex.yml`
- `proofgate/examples/github/ai-author-cursor.yml`
- `proofgate/examples/github/proofgate.yml` for the resulting pull request

The Codex workflow needs `OPENAI_API_KEY`. The Cursor workflow needs
`CURSOR_API_KEY`. A regular GitHub account is enough: use standard
`ubuntu-latest` runners and keep the authoring jobs bounded by their included
Actions allowance. The demo repository must also permit Actions workflow tokens
to write contents and create pull requests. A small authoring task plus the
local ProofGate evaluation should normally fit a 5–10 minute demo window, but
runner queues and model latency are variable; the templates enforce a
20-minute upper bound.

```yaml
- name: Start Entire capture
  uses: ./.github/actions/entire-proofgate
  with:
    mode: begin
    session_id: gha-${{ github.run_id }}-${{ github.run_attempt }}-${{ github.job }}
    prompt: ${{ inputs.task_prompt }}
    provider: claude

- name: Run Claude
  id: claude
  uses: anthropics/claude-code-action@v1
  with:
    anthropic_api_key: ${{ secrets.ANTHROPIC_API_KEY }}
    prompt: ${{ inputs.task_prompt }}

- name: Finish Entire capture
  if: always() && steps.claude.outputs.execution_file != ''
  uses: ./.github/actions/entire-proofgate
  with:
    mode: finish
    provider: claude
    session_id: gha-${{ github.run_id }}-${{ github.run_attempt }}-${{ github.job }}
    execution_file: ${{ steps.claude.outputs.execution_file }}
```

The `begin` step snapshots the repository before the agent changes it. The
`finish` step normalizes the provider execution file, calculates file/token
evidence, and emits TurnEnd and SessionEnd. Keeping both boundaries is what
makes Entire rewind points and final checkpoint attribution trustworthy.

For Codex, set `codex-home` on the official action and pass the newest
`sessions/**/rollout-*.jsonl` file to `execution_file`. For Cursor, redirect
`stream-json` stdout to a runner-temp file and pass that file. Do not echo or
upload either raw transcript as a workflow artifact.

The adapter never prints the raw AI execution transcript. Databricks export is
handled by ProofGate's sanitized Change Passport layer, not by this protocol
binary.
