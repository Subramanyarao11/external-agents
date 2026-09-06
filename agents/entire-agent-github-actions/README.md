# entire-agent-github-actions

External agent binary for capturing AI coding sessions that run inside GitHub
Actions. The first provider integration targets
`anthropics/claude-code-action@v1` and its `session_id` and `execution_file`
outputs.

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

```yaml
- name: Start Entire capture
  uses: ./.github/actions/entire-proofgate
  with:
    mode: begin
    session_id: gha-${{ github.run_id }}-${{ github.run_attempt }}-${{ github.job }}
    prompt: ${{ inputs.task_prompt }}

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
    session_id: gha-${{ github.run_id }}-${{ github.run_attempt }}-${{ github.job }}
    execution_file: ${{ steps.claude.outputs.execution_file }}
```

The `begin` step snapshots the repository before the agent changes it. The
`finish` step copies the native execution file, calculates file/token evidence,
and emits TurnEnd and SessionEnd. Keeping both boundaries is what makes Entire
rewind points and final checkpoint attribution trustworthy.

The adapter never prints the raw AI execution transcript. Databricks export is
handled by ProofGate's sanitized Change Passport layer, not by this protocol
binary.
