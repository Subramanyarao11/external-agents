# Project Structure

## Top-level layout

```
external-agents/
├── agents/                          # External agent adapter implementations
│   ├── entire-agent-amp/            # Amp coding agent
│   ├── entire-agent-goose/          # Goose terminal agent
│   ├── entire-agent-grok/           # Grok Build CLI
│   ├── entire-agent-github-actions/ # GitHub Actions / Claude integration
│   ├── entire-agent-kilo/           # Kilo agent (preview)
│   ├── entire-agent-kiro/           # Kiro IDE/CLI agent
│   ├── entire-agent-omp/            # Oh My Pi interactive agent
│   └── entire-agent-qwen/           # Qwen Code terminal agent
├── proofgate/                       # Deterministic policy engine
│   ├── app/                         # Control Room web UI (Python)
│   ├── cmd/proofgate/               # CLI entry point
│   ├── contracts/                   # Change Passport data types
│   ├── engine/                      # Risk evaluation logic
│   ├── warehouse/                   # Databricks client and event builder
│   ├── passportbuilder/             # Passport construction from checkpoints
│   ├── databricks/                  # SQL schemas and bundle config
│   └── examples/                    # Sample passports and workflows
├── e2e/                             # End-to-end lifecycle test framework
│   ├── agents/                      # Per-agent test adapters
│   ├── bootstrap/                   # Pre-test CI auth setup
│   ├── entire/                      # Entire CLI wrappers
│   └── testutil/                    # Shared test utilities
├── .github/                         # CI/CD
│   ├── workflows/                   # CI, lint, compliance, license workflows
│   └── actions/proofgate-evaluate/  # Packaged GitHub Action
├── doc/                             # License decisions
├── tests/                           # Cross-platform support tests
├── mise-tasks/                      # Build/test/lint task scripts
├── .claude/                         # Claude Code skills and settings
├── .codex/                          # Codex agent configuration
├── .cursor/                         # Cursor IDE rules
├── .opencode/                       # OpenCode plugins
├── .entire/                         # Entire CLI runtime data
├── mise.toml                        # Task runner configuration
├── .golangci.yaml                   # Linter configuration (52 linters)
├── BUILDATHON.md                    # ProofGate buildathon evidence record
├── CLAUDE.md                        # Claude Code project instructions
├── AGENTS.md                        # Agent builder skill entry point
├── CONTRIBUTING.md                  # Contribution guidelines
└── README.md                        # Repository documentation
```

## Agent adapter structure

Each agent follows a consistent layout:

```
agents/entire-agent-{name}/
├── cmd/entire-agent-{name}/
│   └── main.go                  # Binary entry point, subcommand routing
├── internal/{name}/
│   ├── agent.go                 # Agent struct, Info(), Detect(), capabilities
│   ├── hooks.go                 # Lifecycle hook parsing and dispatch
│   ├── transcript.go            # Transcript parsing and analysis
│   ├── capture.go               # Session capture (GitHub Actions only)
│   ├── compact.go               # Compact transcript format (some agents)
│   ├── paths.go                 # File path resolution (some agents)
│   ├── *_test.go                # Unit tests
│   └── curveball_transcript_test.go  # Dual-format tests (GitHub Actions)
├── internal/protocol/
│   ├── protocol.go              # Subcommand handlers (Handle* functions)
│   └── types.go                 # Protocol message types
├── scripts/
│   └── verify-*.sh              # Protocol verification scripts
├── testdata/                    # Test fixtures
├── AGENT.md                     # Detailed capability documentation
├── README.md                    # Setup instructions
├── go.mod                       # Go module definition
└── mise.toml                    # Build/test tasks
```

## Go modules

| Module path | Location | External deps |
|---|---|---|
| `github.com/entireio/external-agents/agents/entire-agent-kiro` | `agents/entire-agent-kiro/` | modernc.org/sqlite |
| `github.com/entireio/external-agents/agents/entire-agent-amp` | `agents/entire-agent-amp/` | none |
| `github.com/entireio/external-agents/agents/entire-agent-goose` | `agents/entire-agent-goose/` | none |
| `github.com/entireio/external-agents/agents/entire-agent-grok` | `agents/entire-agent-grok/` | none |
| `github.com/entireio/external-agents/agents/entire-agent-qwen` | `agents/entire-agent-qwen/` | none |
| `github.com/entireio/external-agents/agents/entire-agent-omp` | `agents/entire-agent-omp/` | none |
| `github.com/entireio/external-agents/agents/entire-agent-kilo` | `agents/entire-agent-kilo/` | none |
| `github.com/entireio/external-agents/agents/entire-agent-github-actions` | `agents/entire-agent-github-actions/` | none |
| `github.com/entireio/external-agents/proofgate` | `proofgate/` | none |
| `github.com/entireio/external-agents/e2e` | `e2e/` | testify |

All modules use Go 1.26.0. Most agent adapters are dependency-free, relying only on the standard library.

## ProofGate directory detail

```
proofgate/
├── cmd/proofgate/
│   ├── main.go              # CLI: evaluate | export | default-policy | ci
│   ├── ci.go                # CI subcommand: build passport + evaluate in GitHub Actions
│   └── ci_test.go           # CI integration tests
├── contracts/
│   └── passport.go          # ChangePassport, RepositoryIdentity, AuthoringEvidence,
│                            # ImpactEvidence, TestEvidence, HistoricalEvidence, SafetyEvidence
├── engine/
│   ├── evaluator.go         # Evaluate(), Decision, Result, RiskReason, HardStop
│   ├── policy.go            # Policy struct, DefaultPolicy()
│   └── evaluator_test.go    # Acceptance scenarios, determinism test
├── passportbuilder/
│   ├── builder.go           # Build() from Entire checkpoint + test evidence
│   └── builder_test.go      # Builder tests (checkpoint, AI signal, secrets, suites)
├── warehouse/
│   ├── client.go            # Databricks SQL client, Config, Ingest(), execute(), poll()
│   ├── event.go             # ChangeEvent, BuildEvent(), allowlist, similarity summary
│   ├── client_test.go       # Client tests (parameterized merge, idempotency)
│   └── event_test.go        # Event tests (field dropping, unsafe export refusal)
├── databricks/
│   ├── databricks.yml       # Databricks Asset Bundle config (jobs, targets, variables)
│   ├── sql/
│   │   ├── 001_setup.sql    # Table creation (bronze, silver, gold, quarantine, similarity)
│   │   └── 002_transform.sql # Data transformation pipeline
│   └── phase2/
│       ├── README.md         # Phase 2 deployment docs
│       ├── databricks.yml    # Extended bundle config
│       └── pipeline/
│           └── proofgate_features.py  # Feature extraction pipeline
├── app/
│   ├── app.py               # Flask Control Room application
│   ├── store.py             # Local data store (SQLite/JSON)
│   ├── lakebase_store.py    # Databricks Lakebase store
│   ├── explainer.py         # Decision explanation generator
│   ├── similarity.py        # Similar change search
│   ├── warehouse_sync.py    # Warehouse synchronization
│   ├── test_store.py        # Store unit tests
│   ├── app.yaml             # Databricks Apps deployment config
│   ├── requirements.txt     # Python dependencies
│   └── static/
│       ├── index.html       # Control Room UI
│       ├── app.js           # Frontend JavaScript
│       └── styles.css       # Styling
├── examples/
│   ├── pass.json            # Example: PASS decision (low risk)
│   ├── approval-required.json # Example: APPROVAL_REQUIRED (high risk)
│   ├── test-report.json     # Example test report
│   └── github/
│       └── proofgate.yml    # Example GitHub Actions workflow
├── go.mod
└── README.md
```

## E2E test framework detail

```
e2e/
├── setup_test.go            # TestMain: agent discovery, binary building, env setup
├── lifecycle_test.go        # Shared lifecycle scenarios (8 test functions)
├── amp_protocol_test.go     # Amp-specific protocol compliance tests
├── omp_session_switch_test.go # OMP session switching via /new command
├── build.go                 # Agent binary discovery and compilation
├── agents/
│   ├── agent.go             # Agent interface, Session interface, registry, concurrency gates
│   ├── kiro.go              # Kiro adapter (concurrency: 2)
│   ├── amp.go               # Amp adapter
│   ├── goose.go             # Goose adapter (concurrency: 2, timeout: 2.0x)
│   ├── grok.go              # Grok adapter
│   ├── qwen.go              # Qwen adapter
│   ├── omp.go               # OMP adapter
│   ├── kilo.go              # Kilo adapter
│   ├── github_actions.go    # GitHub Actions adapter (timeout: 4.0x)
│   ├── tmux.go              # PTY session manager for interactive agents
│   └── omp_test.go          # OMP unit tests
├── entire/
│   └── entire.go            # Entire CLI wrappers (Enable, Disable, Rewind, RewindList)
├── testutil/
│   ├── repo.go              # Test repo setup, ForEachAgent(), RunPrompt(), Git helpers
│   ├── metadata.go          # CheckpointMetadata, SessionMetadata, TokenUsage types
│   ├── artifacts.go         # Artifact capture (git-log, checkpoint metadata, pane content)
│   └── assertions.go        # AssertFileExists, WaitForCheckpoint, ValidateCheckpointDeep
├── bootstrap/
│   └── main.go              # Pre-test agent bootstrap for CI auth setup
└── go.mod
```

## CI/CD configuration

```
.github/
├── workflows/
│   ├── ci.yml                     # Unit test + build for all agents (matrix)
│   ├── lint.yml                   # gofmt + golangci-lint per agent + e2e
│   ├── protocol-compliance.yml    # Shared protocol compliance suite
│   └── license-check.yml          # License verification via entireio/shared
└── actions/
    └── proofgate-evaluate/
        └── action.yml             # Packaged composite action for ProofGate CI
```

## IDE integration files

```
.claude/                           # Claude Code
├── settings.json                  # Project settings
├── settings.local.json            # Local overrides
└── skills/
    └── entire-external-agent/     # Agent building skills
        ├── SKILL.md               # Full pipeline overview
        ├── research.md            # Analyze agent capabilities
        ├── write-tests.md         # Scaffold + wire tests
        └── implement.md           # Build binary (protocol-first)

.codex/                            # Codex
├── config.toml                    # Configuration
└── hooks.json                     # Hook definitions

.cursor/rules/                     # Cursor IDE
└── entire-external-agent.mdc      # Agent building rule

.opencode/plugins/                 # OpenCode
├── entire.ts                      # Entire integration plugin
└── entire-external-agent.js       # Agent discovery script
```
