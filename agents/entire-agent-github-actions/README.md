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
external-agents-tests verify ./entire-agent-github-actions
```

## Installation model

1. Put `entire-agent-github-actions` on the runner `PATH`.
2. Enable `external_agents` in `.entire/settings.json`.
3. Run `entire enable --agent github-actions --telemetry=false`.
4. Reference the repository-local action installed at
   `.github/actions/entire-proofgate/action.yml` after the AI action step.

The adapter never prints the raw AI execution transcript. Databricks export is
handled by ProofGate's sanitized Change Passport layer, not by this protocol
binary.
