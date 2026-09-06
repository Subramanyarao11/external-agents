# Deployment

## Overview

The system has three deployment targets:

1. **Agent binaries** — Go binaries installed locally or in CI runners
2. **GitHub Actions** — ProofGate evaluation action and CI workflows
3. **Databricks** — Evidence pipeline tables and Control Room app

## Agent binary deployment

### Building for distribution

```bash
# Build all agents to ./bin/
mise run build

# Build specific agent
cd agents/entire-agent-github-actions
go build -o entire-agent-github-actions ./cmd/entire-agent-github-actions
```

### Installation

Agent binaries must be in PATH for the Entire CLI to discover them:

```bash
# Copy to a PATH directory
cp ./bin/entire-agent-* /usr/local/bin/

# Enable in a repository
cd /path/to/repo
entire enable --agent github-actions
```

### Requirements

- `external_agents: true` in `.entire/settings.json` (repository-level)
- Entire CLI installed and configured
- Agent binary accessible in PATH

## GitHub Actions deployment

### ProofGate evaluate action

Location: `.github/actions/proofgate-evaluate/action.yml`

This is a composite GitHub Action that runs ProofGate evaluation as a CI check.

**Inputs:**

| Input | Required | Description |
|---|---|---|
| `mode` | yes | `begin` or `finish` |
| `execution_file` | finish only | Path to Claude action output file |
| `session_id` | yes | Stable session ID across begin/finish |
| `prompt` | begin only | Redacted task description |

**Usage in a workflow:**

```yaml
# Example from proofgate/examples/github/proofgate.yml
jobs:
  evaluate:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Begin ProofGate capture
        uses: ./.github/actions/proofgate-evaluate
        with:
          mode: begin
          session_id: ${{ github.run_id }}-${{ github.run_attempt }}
          prompt: ${{ github.event.pull_request.title }}

      # ... Claude Code Action runs here ...

      - name: Finish ProofGate capture
        if: always()
        uses: ./.github/actions/proofgate-evaluate
        with:
          mode: finish
          session_id: ${{ github.run_id }}-${{ github.run_attempt }}
          execution_file: ${{ steps.claude.outputs.execution_file }}
```

### Agent hook installation

The GitHub Actions agent can install its managed action into a repository:

```bash
entire-agent-github-actions install-hooks
# Creates .github/actions/entire-proofgate/action.yml
```

The installed action includes a `# Managed by entire-agent-github-actions` marker for safe updates and uninstallation.

### CI workflows

Four GitHub Actions workflows are defined in `.github/workflows/`:

#### `ci.yml` — Unit tests and builds

- **Trigger:** push to main, PR, manual dispatch
- **Strategy:** Dynamic matrix from agent discovery
- **Steps per agent:** Setup mise → run tests → build binary

#### `lint.yml` — Code quality

- **Trigger:** push to main, PR, manual dispatch
- **Jobs:**
  1. `fmt` — `gofmt -l .` format check
  2. `lint-agents` — golangci-lint per agent (matrix)
  3. `lint-e2e` — golangci-lint on e2e directory

#### `protocol-compliance.yml` — Protocol validation

- **Trigger:** push to main, PR
- **Steps per agent:** Build binary → run `entireio/external-agents-tests` action

#### `license-check.yml` — License compliance

- **Trigger:** push to main, PR, manual dispatch
- **Delegates to:** `entireio/shared/.github/workflows/license-check-reusable.yml@main`

### Pinned action versions

All third-party actions use commit SHA pins for supply chain security:

| Action | Pin |
|---|---|
| `actions/checkout` | `de0fac2e4500dabe0009e67214ff5f5447ce83dd` (v6.0.2) |
| `jdx/mise-action` | `c37c93293d6b742fc901e1406b8f764f6fb19dac` (v2.4.4) |
| `actions/setup-go` | `4a3601121dd01d1626a1e23e37211e3254c1c06c` (v6.4.0) |
| `golangci/golangci-lint-action` | `1e7e51e771db61008b38414a730f564565cf7c20` (v9.2.0) |
| `entireio/external-agents-tests` | `3220ca8cc7ba2fbfc5a951ce4a5937ca1a5ca26e` |

## Databricks deployment

### Prerequisites

- Databricks workspace with Unity Catalog enabled
- SQL warehouse provisioned
- Databricks CLI installed and authenticated
- Service principal or PAT with appropriate permissions

### Bundle deployment

Source: `proofgate/databricks/databricks.yml`

```bash
# Deploy to dev (default target)
cd proofgate/databricks
databricks bundle deploy --target dev

# Deploy to demo (production)
databricks bundle deploy --target demo

# Run the setup job
databricks bundle run proofgate_prepare --target dev
```

**Variables to configure:**

| Variable | Default | Description |
|---|---|---|
| `catalog` | `main` | Unity Catalog name |
| `schema` | `proofgate_dev` | Schema name |
| `warehouse_id` | (required) | SQL warehouse ID |

### Phase 2 deployment

Source: `proofgate/databricks/phase2/`

Extended pipeline with feature extraction and similarity search:

```bash
cd proofgate/databricks/phase2
databricks bundle deploy --target dev
```

### Table setup

The `proofgate_prepare` job runs two SQL tasks in order:

1. **setup_tables** (`001_setup.sql`) — Creates all tables if not exists
2. **transform_evidence** (`002_transform.sql`) — Sets up transformation pipeline

Tables created:
- `bronze_checkpoint_events`
- `silver_checkpoint_events`
- `silver_quarantine`
- `gold_change_risk_features`
- `similar_change_documents`
- `policy_decision_events`
- `fleet_gate_metrics` (view)

### Control Room deployment

Source: `proofgate/app/`

#### Local development

```bash
cd proofgate/app
pip install -r requirements.txt
python app.py
```

#### Databricks Apps deployment

The `app.yaml` configures deployment to Databricks Apps:

```yaml
# proofgate/app/app.yaml
command: ["python", "app.py"]
```

```bash
# Deploy to Databricks Apps
databricks apps deploy proofgate-control-room --source-code-path proofgate/app
```

### Verifying deployment

#### Offline verification (no credentials needed)

```bash
# ProofGate CLI
cd proofgate
go run ./cmd/proofgate evaluate --passport examples/pass.json --now 2026-09-04T12:00:00Z
go run ./cmd/proofgate export --passport examples/pass.json --dry-run --now 2026-09-04T12:00:00Z

# GitHub Actions agent
./agents/entire-agent-github-actions/scripts/verify-github-actions.sh --sample
```

#### Live verification (requires credentials)

```bash
# Export to Databricks
DATABRICKS_HOST=https://workspace.cloud.databricks.com \
DATABRICKS_TOKEN=dapi... \
DATABRICKS_SQL_WAREHOUSE_ID=abc123 \
PROOFGATE_DATABRICKS_CATALOG=main \
PROOFGATE_DATABRICKS_SCHEMA=proofgate_dev \
go run ./cmd/proofgate export --passport examples/pass.json

# Verify tables
databricks sql query "SELECT COUNT(*) FROM main.proofgate_dev.bronze_checkpoint_events"
```
