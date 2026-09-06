from __future__ import annotations

import sys
import unittest
from pathlib import Path


sys.path.insert(0, str(Path(__file__).resolve().parent))

from publish_pr_report import MARKER, render_report  # noqa: E402


class PRReportTests(unittest.TestCase):
    def test_report_contains_rich_allowlisted_evidence(self) -> None:
        report = render_report(
            {
                "passport": {
                    "change": {"commit_sha": "abc1234", "checkpoint_id": "cp-1"},
                    "authoring": {
                        "agent_family": "codex",
                        "model_family": "gpt",
                        "session_count": 2,
                        "handoff_count": 1,
                        "tool_categories": ["search", "shell"],
                        "provenance_complete": True,
                    },
                    "impact": {
                        "changed_files": ["one.go", "two.go"],
                        "changed_line_count": 42,
                        "impacted_entities": ["A", "B", "C"],
                        "dependency_depth": 3,
                        "max_dependent_count": 17,
                        "analysis_source": "entire-graph-v0.4.0",
                        "analysis_complete": True,
                    },
                    "tests": {"total": 12, "failed": 1, "required_test_missing": False},
                    "history": {
                        "available": True,
                        "baseline_change_count": 9,
                        "baseline_window_days": 30,
                        "similar_failure_rate": 0.25,
                    },
                    "safety": {"redaction_version": "allowlist-v1", "dropped_field_count": 5},
                },
                "result": {
                    "decision": "APPROVAL_REQUIRED",
                    "score": 82,
                    "policy_version": "policy-v1",
                    "passport_fingerprint": "sha256:fingerprint",
                    "hard_stops": [{"code": "STOP", "message": "Review it."}],
                    "risk_reasons": [{"code": "RISK", "points": 20, "message": "Broad impact."}],
                },
                "enforcement_status": "AWAITING_APPROVAL",
                "databricks": {"status": "ROUNDTRIP_COMPLETE"},
            },
            "https://github.example/run/1",
        )
        for expected in (
            MARKER,
            "AWAITING APPROVAL",
            "`cp-1`",
            "codex / gpt",
            "entire-graph-v0.4.0",
            "2 files · 42 lines · 3 impacted entities",
            "11/12 passed",
            "similar failure 25%",
            "ROUNDTRIP_COMPLETE",
            "No raw prompts",
        ):
            self.assertIn(expected, report)

    def test_markdown_table_values_are_escaped(self) -> None:
        report = render_report(
            {"passport": {"change": {"commit_sha": "safe|cell\nnext"}}},
            "https://github.example/run/1",
        )
        self.assertIn("safe\\|cell next", report)

    def test_missing_checkpoint_remains_reviewable_in_databricks(self) -> None:
        transform = (
            Path(__file__).resolve().parents[1] / "databricks" / "sql" / "002_transform.sql"
        ).read_text(encoding="utf-8")
        quarantine, silver = transform.split("MERGE INTO silver_checkpoint_events", 1)
        self.assertNotIn("MISSING_CHECKPOINT_ID", quarantine)
        self.assertIn(
            "coalesce(nullif(trim(checkpoint_id), ''), 'missing') AS checkpoint_id",
            silver,
        )
        self.assertNotIn("AND checkpoint_id IS NOT NULL", silver)


if __name__ == "__main__":
    unittest.main()
