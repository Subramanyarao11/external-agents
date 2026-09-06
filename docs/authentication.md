# Authentication & Authorization

## Overview

The system has multiple authentication boundaries depending on the component.

## Databricks authentication

**Used by:** ProofGate warehouse client (`proofgate/warehouse/client.go`)

| Variable | Description |
|---|---|
| `DATABRICKS_HOST` | HTTPS workspace URL (e.g., `https://workspace.cloud.databricks.com`) |
| `DATABRICKS_TOKEN` | Bearer API token (PAT or service principal token) |
| `DATABRICKS_SQL_WAREHOUSE_ID` | SQL warehouse to execute statements against |
| `PROOFGATE_DATABRICKS_CATALOG` | Unity Catalog for evidence tables |
| `PROOFGATE_DATABRICKS_SCHEMA` | Schema within the catalog |

**Enforcement:**
- The client validates that `Host` uses HTTPS before making any request (`proofgate/warehouse/client.go:NewClient`)
- All fields are required; missing any returns an error
- Token is passed as `Authorization: Bearer <token>` header
- HTTP client timeout: 55 seconds
- Response body capped at 2 MB

**No credentials are stored in code or committed to the repository.** All authentication is via environment variables.

## GitHub Actions authentication

**Used by:** GitHub Actions agent (`agents/entire-agent-github-actions/`)

The agent reads GitHub-provided environment variables for metadata collection, not for authentication:

| Variable | Purpose |
|---|---|
| `GITHUB_ACTIONS` | Detection: `true` means running in Actions |
| `GITHUB_REPOSITORY` | Repository identifier for metadata |
| `GITHUB_RUN_ID` | Workflow run ID for session correlation |
| `GITHUB_RUN_ATTEMPT` | Run attempt number |
| `GITHUB_SHA` | Commit SHA for provenance |
| `GITHUB_WORKFLOW` | Workflow name |
| `GITHUB_JOB` | Job name |

The GitHub Actions agent does not perform GitHub API calls directly. GitHub API authentication (for PR checks, status updates) is handled by the GitHub Actions workflow using `GITHUB_TOKEN` or repository secrets.

## Entire CLI authentication

**Used by:** Hook dispatch in agent adapters

The agent adapters dispatch lifecycle hooks via the Entire CLI binary:

```bash
entire hooks github-actions <hook-name>
```

The Entire CLI manages its own authentication (keyring-based). Agent adapters do not handle Entire credentials — they invoke the CLI and pass hook payloads on stdin.

- `ENTIRE_CLI_PATH` — Optional override for the Entire binary path (default: `entire` from `PATH`)
- `ENTIRE_REPO_ROOT` — Repository root for session scoping

## Control Room authentication

**TODO: Needs verification** — The Control Room app (`proofgate/app/app.py`) authentication mechanism depends on deployment context (local dev vs. Databricks Apps). The Databricks Apps deployment (`app.yaml`) may use workspace-level authentication.

## Security boundaries

### What is trusted

| Input | Trust level | Source |
|---|---|---|
| Git repository state | Trusted | Verifiable via SHA |
| Entire checkpoints | Trusted | Cryptographically tracked |
| Test execution results | Trusted | Runner-produced artifacts |
| Policy configuration | Trusted | Committed to repository |
| Approval receipts | Trusted | Validated structure + actor |
| Databricks API responses | Trusted | Authenticated HTTPS |

### What is untrusted

| Input | Why untrusted |
|---|---|
| PR description text | User-authored, not verified |
| Agent self-reported claims | Model output, not verifiable |
| Arbitrary workflow inputs | External, unvalidated |
| Unverified checkpoint refs | Must be structurally validated |
| Unsigned approval assertions | Must have valid receipt structure |

### Export safety

The `BuildEvent()` function (`proofgate/warehouse/event.go`) enforces safety before any data leaves the runner:

1. **Repository must be opted in** (`Repository.OptedIn == true`)
2. **No secrets detected** (`Safety.SecretDetected == false`)
3. **Raw content requires consent** (if `RawContentExported`, must have `ExplicitContentConsent`)

Violations produce hard stops that block both the merge decision and the export.

### Redaction

Every export applies the `proofgate-allowlist-v1` redaction policy:

- File paths → replaced by `changed_file_count`
- Entity names → replaced by `impacted_entity_count`
- The `dropped_field_count` increases to reflect redacted information
- Raw source code, prompts, and chain-of-thought are never included in the event structure

## Session file permissions

Agent adapters write session files with restricted permissions:

| Resource | Permission | Code reference |
|---|---|---|
| Session directory | `0o700` (owner only) | `capture.go` |
| Session files | `0o600` (owner read-write) | `capture.go` |
| Installed hooks | `0o600` (file), `0o750` (directory) | `hooks.go` |

Session files are written atomically (temp file + rename) to prevent partial reads.
