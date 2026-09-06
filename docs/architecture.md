# Architecture

## System overview

The system comprises three layers: **agent adapters** that capture AI coding sessions, a **deterministic policy engine** that evaluates evidence and produces decisions, and an **evidence pipeline** that persists sanitized results for historical analysis.

```mermaid
flowchart TB
    subgraph Agents["Agent Adapters (Go binaries)"]
        K[Kiro] & A[Amp] & Go[Goose] & Gr[Grok] & Q[Qwen] & O[OMP] & Ki[Kilo] & GH[GitHub Actions]
    end

    subgraph Entire["Entire CLI"]
        E[Checkpoint Engine]
        G[Graph Analysis]
        H[Hook Dispatch]
    end

    subgraph ProofGate["ProofGate"]
        PB[Passport Builder]
        PE[Policy Engine]
        WH[Warehouse Client]
        CR[Control Room UI]
    end

    subgraph Infra["Infrastructure"]
        GA[GitHub Actions CI]
        DB[(Databricks Unity Catalog)]
    end

    Agents -->|lifecycle hooks| H
    Agents -->|transcript data| E
    E -->|checkpoints| PB
    G -->|impact analysis| PB
    PB -->|Change Passport| PE
    PE -->|PASS/WARN/APPROVAL_REQUIRED| GA
    PE -->|sanitized event| WH
    WH -->|MERGE INTO| DB
    DB -->|historical baseline| PE
    GA -->|blocked PR| CR
    CR -->|approval receipt| PE
```

## External agent protocol

Every agent adapter implements the same stdin/stdout JSON protocol. The Entire CLI invokes agent binaries as subprocesses with specific subcommands.

```mermaid
sequenceDiagram
    participant CLI as Entire CLI
    participant Agent as Agent Binary
    participant FS as File System

    CLI->>Agent: info
    Agent-->>CLI: InfoResponse (capabilities, hooks, protected files)

    CLI->>Agent: detect
    Agent-->>CLI: DetectResponse (present: true/false)

    CLI->>Agent: get-session-dir --repo-path /path
    Agent-->>CLI: SessionDirResponse

    Note over CLI,Agent: During coding session
    CLI->>Agent: parse-hook --hook turn-start (stdin: HookInputJSON)
    Agent-->>CLI: EventJSON (type: 2, prompt, metadata)

    CLI->>Agent: parse-hook --hook turn-end (stdin: HookInputJSON)
    Agent-->>CLI: EventJSON (type: 3, response, model)

    Note over CLI,Agent: After session
    CLI->>Agent: get-transcript-position --path /session.json
    Agent-->>CLI: TranscriptPositionResponse (count)

    CLI->>Agent: extract-modified-files --path /session.json --offset 0
    Agent-->>CLI: ExtractFilesResponse (files, current)

    CLI->>Agent: compact-transcript --session-ref /session.json
    Agent-->>CLI: CompactTranscriptResponse (base64 JSONL)
```

### Protocol message types

| Type ID | Name | Direction | Description |
|---|---|---|---|
| 1 | SessionStart (`run-start`) | Agent → CLI | Session begins |
| 2 | TurnStart (`turn-start`) | Agent → CLI | User prompt received |
| 3 | TurnEnd (`turn-end`) | Agent → CLI | Agent response complete |
| 5 | SessionEnd (`run-end`) | Agent → CLI | Session finished |

### Agent capabilities

Each agent declares capabilities in its `info` response:

| Capability | Description |
|---|---|
| `hooks` | Lifecycle event handlers (start, stop, commit) |
| `transcript_analyzer` | Extract files, prompts, summaries from transcripts |
| `token_calculator` | Count input/output/cache tokens |
| `compact_transcript` | Produce base64-encoded JSONL compact format |
| `transcript_preparer` | Format transcripts for display |
| `text_generator` | Generate agent-specific output |
| `hook_response_writer` | Custom hook responses |
| `subagent_aware_extractor` | Handle nested agent sessions |

## ProofGate decision flow

```mermaid
flowchart TD
    A[Change Passport JSON] --> B[Validate Schema]
    B --> C[Compute SHA256 Fingerprint]
    C --> D{Check Hard Stops}

    D -->|Secret detected| X[APPROVAL_REQUIRED]
    D -->|Repo not opted in| X
    D -->|Content export without consent| X
    D -->|Denied component| X
    D -->|Required test missing| X
    D -->|Test failure| X
    D -->|AI change, no checkpoint| X

    D -->|No hard stops| E[Accumulate Risk Score]

    E --> F[Provenance check: +35 if incomplete]
    F --> G[Handoff check: +10 if excessive]
    G --> H[Sensitive component: +20 per component]
    H --> I[Dependency depth: +10 if deep]
    I --> J[Test evidence: +25 if none]
    J --> K[Historical checks: file count, blast radius, similar failures, component rate, repeated patterns]

    K --> L{Score vs thresholds}
    L -->|Score <= 29| M[PASS]
    L -->|30 <= Score <= 59| N[WARN]
    L -->|Score >= 60| O[APPROVAL_REQUIRED]
```

### Decision types

| Decision | Meaning | Action |
|---|---|---|
| `PASS` | Risk score within acceptable range, no hard stops | Auto-approve merge |
| `WARN` | Elevated risk, review recommended | Merge allowed with warnings |
| `APPROVAL_REQUIRED` | Hard stop triggered or high risk score | Block until human review |

## GitHub Actions integration flow

```mermaid
sequenceDiagram
    participant Dev as Developer
    participant GH as GitHub Actions
    participant Agent as Claude Code Action
    participant EA as entire-agent-github-actions
    participant PG as ProofGate Action
    participant CR as Control Room

    Dev->>GH: Push / PR
    GH->>Agent: Run Claude Code Action
    Agent->>EA: capture-start --session-id --prompt
    EA-->>Agent: run-start, turn-start hooks
    Note over Agent: Claude performs work
    Agent->>EA: capture-finish --execution-file --session-id
    EA-->>Agent: turn-end, run-end hooks + evidence

    GH->>PG: proofgate-evaluate action
    PG->>PG: Build Change Passport
    PG->>PG: Evaluate against policy
    PG-->>GH: Decision + reasons

    alt PASS
        GH->>GH: PR check passes
    else APPROVAL_REQUIRED
        GH->>GH: PR check blocks
        GH->>CR: Display in Control Room
        CR->>CR: Human reviews evidence
        CR-->>GH: Approval receipt
        GH->>PG: Re-evaluate with receipt
        PG-->>GH: PASS (with approval)
    end
```

## Databricks evidence pipeline

```mermaid
flowchart LR
    subgraph Runner["GitHub Actions Runner"]
        E[ProofGate CLI]
    end

    subgraph Databricks["Databricks Unity Catalog"]
        B[(bronze_checkpoint_events)]
        S[(silver_checkpoint_events)]
        Q[(silver_quarantine)]
        G[(gold_change_risk_features)]
        SIM[(similar_change_documents)]
        POL[(policy_decision_events)]
        V[fleet_gate_metrics VIEW]
    end

    E -->|MERGE INTO| B
    B -->|validate + transform| S
    B -->|invalid events| Q
    S -->|extract features| G
    S -->|similarity index| SIM
    G --> V
```

### Data tiers

| Tier | Table | Purpose |
|---|---|---|
| Bronze | `bronze_checkpoint_events` | Raw allowlisted events from runners |
| Silver | `silver_checkpoint_events` | Validated, deduplicated events |
| Silver | `silver_quarantine` | Invalid events with quarantine reasons |
| Gold | `gold_change_risk_features` | Extracted features for analytics/ML |
| Gold | `similar_change_documents` | Similarity search index |
| Audit | `policy_decision_events` | Human review decisions |
| View | `fleet_gate_metrics` | Aggregated daily metrics |

## Privacy boundary

```mermaid
flowchart LR
    subgraph Trusted["Runner (trusted boundary)"]
        P[Full Change Passport]
        R[Raw file paths]
        E[Entity names]
        S[Source code]
    end

    subgraph Exported["Databricks (exported)"]
        C[Counts only]
        D[Decision codes]
        M[Categories]
        F[Fingerprints]
    end

    subgraph Blocked["Never exported"]
        X1[Secrets]
        X2[Prompts]
        X3[Chain-of-thought]
        X4[Raw content without consent]
    end

    P -->|allowlist filter| C
    P -->|allowlist filter| D
    P -->|allowlist filter| M
    P -->|allowlist filter| F
    P -.-x X1
    P -.-x X2
    P -.-x X3
    P -.-x X4
```

**Allowlisted fields** (safe to export):
- Counts: file count, entity count, line count, session count
- Codes: risk reason codes, hard stop codes, decision
- Categories: agent family, model family, tool categories, sensitive components
- Identifiers: event ID, commit SHA, checkpoint ID, repo ID (synthetic)
- Metadata: timestamps, policy version, redaction version, fingerprints
- Similarity: safe categorized summary (e.g., `agent=claude decision=PASS blast_radius=small`)

**Redacted fields** (dropped, counted in `dropped_field_count`):
- Individual file paths → replaced by `changed_file_count`
- Individual entity names → replaced by `impacted_entity_count`
- Raw source code, prompts, chain-of-thought: never included

## Control Room architecture

The Control Room (`proofgate/app/`) is a Python web application for human review of blocked changes.

```mermaid
flowchart TB
    subgraph UI["Web UI (Flask)"]
        H[Static HTML/JS/CSS]
        API[REST API endpoints]
    end

    subgraph Backend["Python Backend"]
        APP[app.py - Flask routes]
        EXP[explainer.py - Decision explanations]
        SIM[similarity.py - Similar change lookup]
        WS[warehouse_sync.py - Databricks sync]
    end

    subgraph Storage["Data Layer"]
        LS[store.py - Local SQLite/JSON store]
        LB[lakebase_store.py - Databricks Lakebase]
    end

    H --> API
    API --> APP
    APP --> EXP
    APP --> SIM
    APP --> WS
    APP --> LS
    APP --> LB
    WS --> LB
```
