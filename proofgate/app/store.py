"""Transactional storage for the ProofGate review control room.

The local implementation deliberately uses only Python's standard library so
the demo works without a network connection.  Its schema and API mirror the
Lakebase-backed production store: immutable automated evidence, optimistic
review versions, and idempotent human decisions.
"""

from __future__ import annotations

import json
import sqlite3
import threading
import uuid
from contextlib import contextmanager
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Iterator


class GateNotFoundError(KeyError):
    pass


class ReviewConflictError(RuntimeError):
    def __init__(self, current_version: int):
        super().__init__(f"gate was already updated; current version is {current_version}")
        self.current_version = current_version


class ValidationError(ValueError):
    pass


def _now() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="seconds")


SEED_GATES: tuple[dict[str, Any], ...] = (
    {
        "event_id": "gate-checkout-retry",
        "repo_id": "mobile-checkout",
        "checkpoint_id": "01PROOFGATECHECKOUT",
        "commit_sha": "71a0c42",
        "automated_verdict": "APPROVAL_REQUIRED",
        "risk_score": 88,
        "changed_file_count": 7,
        "impact_analysis_source": "entire-graph-v0.4.0",
        "impact_analysis_complete": True,
        "max_dependent_count": 24,
        "tests_total": 14,
        "tests_failed": 1,
        "provenance_complete": True,
        "summary": "Checkout retry behavior changed near a payment boundary.",
        "reason_codes": ["REQUIRED_TEST_FAILED", "HIGH_RISK_COMPONENT"],
        "hard_stop_codes": ["REQUIRED_TEST_FAILED"],
        "review_status": "PENDING",
        "occurred_at": "2026-09-04T08:12:00+00:00",
    },
    {
        "event_id": "gate-auth-refresh",
        "repo_id": "identity-service",
        "checkpoint_id": "01PROOFGATEAUTH",
        "commit_sha": "af2019d",
        "automated_verdict": "APPROVAL_REQUIRED",
        "risk_score": 73,
        "changed_file_count": 4,
        "impact_analysis_source": "entire-graph-v0.4.0",
        "impact_analysis_complete": False,
        "max_dependent_count": 11,
        "tests_total": 31,
        "tests_failed": 0,
        "provenance_complete": False,
        "summary": "Token refresh logic has incomplete agent provenance.",
        "reason_codes": ["PROVENANCE_INCOMPLETE", "SENSITIVE_COMPONENT"],
        "hard_stop_codes": ["PROVENANCE_INCOMPLETE"],
        "review_status": "PENDING",
        "occurred_at": "2026-09-04T07:46:00+00:00",
    },
    {
        "event_id": "gate-search-empty-state",
        "repo_id": "consumer-app",
        "checkpoint_id": "01PROOFGATESEARCH",
        "commit_sha": "d3116be",
        "automated_verdict": "WARN",
        "risk_score": 34,
        "changed_file_count": 5,
        "impact_analysis_source": "entire-graph-v0.4.0",
        "impact_analysis_complete": True,
        "max_dependent_count": 8,
        "tests_total": 22,
        "tests_failed": 0,
        "provenance_complete": True,
        "summary": "Search empty-state behavior changed with matching test evidence.",
        "reason_codes": ["SIMILAR_CHANGE_FAILURE_HISTORY"],
        "hard_stop_codes": [],
        "review_status": "PENDING",
        "occurred_at": "2026-09-04T06:20:00+00:00",
    },
    {
        "event_id": "gate-profile-copy",
        "repo_id": "consumer-app",
        "checkpoint_id": "01PROOFGATEPROFILE",
        "commit_sha": "48ca9e1",
        "automated_verdict": "PASS",
        "risk_score": 8,
        "changed_file_count": 2,
        "impact_analysis_source": "entire-graph-v0.4.0",
        "impact_analysis_complete": True,
        "max_dependent_count": 2,
        "tests_total": 9,
        "tests_failed": 0,
        "provenance_complete": True,
        "summary": "Profile helper copy changed with complete provenance and tests.",
        "reason_codes": [],
        "hard_stop_codes": [],
        "review_status": "AUTO_PASS",
        "occurred_at": "2026-09-04T05:05:00+00:00",
    },
)


class SQLiteStore:
    """SQLite demo store with production-grade decision invariants."""

    def __init__(self, path: str | Path):
        self.path = str(path)
        self._schema_lock = threading.Lock()
        self.initialize()

    @contextmanager
    def _connection(self) -> Iterator[sqlite3.Connection]:
        connection = sqlite3.connect(self.path, timeout=10, isolation_level=None)
        connection.row_factory = sqlite3.Row
        connection.execute("PRAGMA foreign_keys = ON")
        connection.execute("PRAGMA busy_timeout = 10000")
        try:
            yield connection
        finally:
            connection.close()

    def initialize(self) -> None:
        with self._schema_lock, self._connection() as connection:
            connection.executescript(
                """
                CREATE TABLE IF NOT EXISTS gates (
                    event_id TEXT PRIMARY KEY,
                    repo_id TEXT NOT NULL,
                    checkpoint_id TEXT NOT NULL,
                    commit_sha TEXT NOT NULL,
                    automated_verdict TEXT NOT NULL,
                    risk_score INTEGER NOT NULL CHECK (risk_score BETWEEN 0 AND 100),
                    changed_file_count INTEGER NOT NULL,
                    impact_analysis_source TEXT NOT NULL DEFAULT 'git-path-heuristic',
                    impact_analysis_complete INTEGER NOT NULL DEFAULT 0,
                    max_dependent_count INTEGER NOT NULL DEFAULT 0,
                    tests_total INTEGER NOT NULL,
                    tests_failed INTEGER NOT NULL,
                    provenance_complete INTEGER NOT NULL,
                    summary TEXT NOT NULL,
                    reason_codes_json TEXT NOT NULL,
                    hard_stop_codes_json TEXT NOT NULL,
                    review_status TEXT NOT NULL,
                    version INTEGER NOT NULL DEFAULT 1,
                    occurred_at TEXT NOT NULL,
                    updated_at TEXT NOT NULL
                );

                CREATE TABLE IF NOT EXISTS decisions (
                    decision_id TEXT PRIMARY KEY,
                    gate_event_id TEXT NOT NULL REFERENCES gates(event_id),
                    action TEXT NOT NULL CHECK (action IN ('APPROVE', 'REJECT')),
                    actor_id TEXT NOT NULL,
                    reason TEXT NOT NULL,
                    previous_version INTEGER NOT NULL,
                    resulting_version INTEGER NOT NULL,
                    idempotency_key TEXT NOT NULL UNIQUE,
                    occurred_at TEXT NOT NULL
                );

                CREATE INDEX IF NOT EXISTS decisions_gate_event_id_idx
                    ON decisions(gate_event_id, occurred_at DESC);
                """
            )
            columns = {
                row["name"] for row in connection.execute("PRAGMA table_info(gates)")
            }
            migrations = {
                "impact_analysis_source": "TEXT NOT NULL DEFAULT 'git-path-heuristic'",
                "impact_analysis_complete": "INTEGER NOT NULL DEFAULT 0",
                "max_dependent_count": "INTEGER NOT NULL DEFAULT 0",
            }
            for name, definition in migrations.items():
                if name not in columns:
                    connection.execute(f"ALTER TABLE gates ADD COLUMN {name} {definition}")

    def seed_demo(self, *, reset: bool = False) -> dict[str, int]:
        with self._connection() as connection:
            connection.execute("BEGIN IMMEDIATE")
            if reset:
                connection.execute("DELETE FROM decisions")
                connection.execute("DELETE FROM gates")
            inserted = 0
            for gate in SEED_GATES:
                cursor = connection.execute(
                    """
                    INSERT OR IGNORE INTO gates (
                        event_id, repo_id, checkpoint_id, commit_sha,
                        automated_verdict, risk_score, changed_file_count,
                        impact_analysis_source, impact_analysis_complete, max_dependent_count,
                        tests_total, tests_failed, provenance_complete, summary,
                        reason_codes_json, hard_stop_codes_json, review_status,
                        version, occurred_at, updated_at
                    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
                    """,
                    (
                        gate["event_id"], gate["repo_id"], gate["checkpoint_id"],
                        gate["commit_sha"], gate["automated_verdict"],
                        gate["risk_score"], gate["changed_file_count"],
                        gate["impact_analysis_source"],
                        int(gate["impact_analysis_complete"]),
                        gate["max_dependent_count"],
                        gate["tests_total"], gate["tests_failed"],
                        int(gate["provenance_complete"]), gate["summary"],
                        json.dumps(gate["reason_codes"]),
                        json.dumps(gate["hard_stop_codes"]), gate["review_status"],
                        gate["occurred_at"], _now(),
                    ),
                )
                inserted += cursor.rowcount
            connection.execute("COMMIT")
        return {"inserted": inserted, "total": len(SEED_GATES)}

    def upsert_gates(self, gates: list[dict[str, Any]]) -> dict[str, int]:
        """Refresh machine evidence without mutating human review columns."""
        with self._connection() as connection:
            connection.execute("BEGIN IMMEDIATE")
            for gate in gates:
                connection.execute(
                    """
                    INSERT INTO gates (
                        event_id, repo_id, checkpoint_id, commit_sha,
                        automated_verdict, risk_score, changed_file_count,
                        impact_analysis_source, impact_analysis_complete, max_dependent_count,
                        tests_total, tests_failed, provenance_complete, summary,
                        reason_codes_json, hard_stop_codes_json, review_status,
                        version, occurred_at, updated_at
                    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
                    ON CONFLICT(event_id) DO UPDATE SET
                        repo_id = excluded.repo_id,
                        checkpoint_id = excluded.checkpoint_id,
                        commit_sha = excluded.commit_sha,
                        automated_verdict = excluded.automated_verdict,
                        risk_score = excluded.risk_score,
                        changed_file_count = excluded.changed_file_count,
                        impact_analysis_source = excluded.impact_analysis_source,
                        impact_analysis_complete = excluded.impact_analysis_complete,
                        max_dependent_count = excluded.max_dependent_count,
                        tests_total = excluded.tests_total,
                        tests_failed = excluded.tests_failed,
                        provenance_complete = excluded.provenance_complete,
                        summary = excluded.summary,
                        reason_codes_json = excluded.reason_codes_json,
                        hard_stop_codes_json = excluded.hard_stop_codes_json,
                        occurred_at = excluded.occurred_at,
                        updated_at = excluded.updated_at
                    """,
                    (
                        gate["event_id"], gate["repo_id"], gate["checkpoint_id"],
                        gate["commit_sha"], gate["automated_verdict"], gate["risk_score"],
                        gate["changed_file_count"],
                        gate.get("impact_analysis_source", "git-path-heuristic"),
                        int(gate.get("impact_analysis_complete", False)),
                        int(gate.get("max_dependent_count", 0)),
                        gate["tests_total"], gate["tests_failed"],
                        int(gate["provenance_complete"]), gate["summary"],
                        json.dumps(gate["reason_codes"]), json.dumps(gate["hard_stop_codes"]),
                        gate.get("review_status", "PENDING"), gate["occurred_at"], _now(),
                    ),
                )
            connection.execute("COMMIT")
        return {"upserted": len(gates)}

    @staticmethod
    def _gate_from_row(row: sqlite3.Row) -> dict[str, Any]:
        gate = dict(row)
        gate["provenance_complete"] = bool(gate["provenance_complete"])
        gate["impact_analysis_complete"] = bool(gate["impact_analysis_complete"])
        gate["reason_codes"] = json.loads(gate.pop("reason_codes_json"))
        gate["hard_stop_codes"] = json.loads(gate.pop("hard_stop_codes_json"))
        return gate

    @staticmethod
    def _decision_from_row(row: sqlite3.Row) -> dict[str, Any]:
        return dict(row)

    def list_gates(self, status: str | None = None) -> list[dict[str, Any]]:
        query = "SELECT * FROM gates"
        parameters: tuple[Any, ...] = ()
        if status:
            query += " WHERE review_status = ?"
            parameters = (status,)
        query += " ORDER BY occurred_at DESC"
        with self._connection() as connection:
            rows = connection.execute(query, parameters).fetchall()
        return [self._gate_from_row(row) for row in rows]

    def get_gate(self, event_id: str) -> dict[str, Any]:
        with self._connection() as connection:
            row = connection.execute(
                "SELECT * FROM gates WHERE event_id = ?", (event_id,)
            ).fetchone()
            if row is None:
                raise GateNotFoundError(event_id)
            decisions = connection.execute(
                "SELECT * FROM decisions WHERE gate_event_id = ? ORDER BY occurred_at DESC",
                (event_id,),
            ).fetchall()
        gate = self._gate_from_row(row)
        gate["decisions"] = [self._decision_from_row(item) for item in decisions]
        return gate

    def decide(
        self,
        event_id: str,
        *,
        action: str,
        actor_id: str,
        reason: str,
        expected_version: int,
        idempotency_key: str,
    ) -> dict[str, Any]:
        action = action.strip().upper()
        actor_id = actor_id.strip()
        reason = reason.strip()
        idempotency_key = idempotency_key.strip()
        if action not in {"APPROVE", "REJECT"}:
            raise ValidationError("action must be APPROVE or REJECT")
        if not actor_id or len(actor_id) > 120:
            raise ValidationError("actor_id is required and must be at most 120 characters")
        if not reason or len(reason) > 500:
            raise ValidationError("reason is required and must be at most 500 characters")
        if not idempotency_key or len(idempotency_key) > 160:
            raise ValidationError("idempotency_key is required and must be at most 160 characters")
        if not isinstance(expected_version, int) or expected_version < 1:
            raise ValidationError("expected_version must be a positive integer")

        with self._connection() as connection:
            connection.execute("BEGIN IMMEDIATE")
            existing = connection.execute(
                "SELECT * FROM decisions WHERE idempotency_key = ?",
                (idempotency_key,),
            ).fetchone()
            if existing is not None:
                decision = self._decision_from_row(existing)
                if decision["gate_event_id"] != event_id:
                    connection.execute("ROLLBACK")
                    raise ValidationError("idempotency_key was already used for a different gate")
                connection.execute("COMMIT")
                return {"decision": decision, "idempotent_replay": True}

            row = connection.execute(
                "SELECT version FROM gates WHERE event_id = ?", (event_id,)
            ).fetchone()
            if row is None:
                connection.execute("ROLLBACK")
                raise GateNotFoundError(event_id)
            if row["version"] != expected_version:
                current_version = int(row["version"])
                connection.execute("ROLLBACK")
                raise ReviewConflictError(current_version)

            resulting_version = expected_version + 1
            occurred_at = _now()
            decision = {
                "decision_id": str(uuid.uuid4()),
                "gate_event_id": event_id,
                "action": action,
                "actor_id": actor_id,
                "reason": reason,
                "previous_version": expected_version,
                "resulting_version": resulting_version,
                "idempotency_key": idempotency_key,
                "occurred_at": occurred_at,
            }
            connection.execute(
                """
                INSERT INTO decisions (
                    decision_id, gate_event_id, action, actor_id, reason,
                    previous_version, resulting_version, idempotency_key, occurred_at
                ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
                """,
                tuple(decision.values()),
            )
            connection.execute(
                """
                UPDATE gates
                SET review_status = ?, version = ?, updated_at = ?
                WHERE event_id = ? AND version = ?
                """,
                (
                    "APPROVED" if action == "APPROVE" else "REJECTED",
                    resulting_version,
                    occurred_at,
                    event_id,
                    expected_version,
                ),
            )
            connection.execute("COMMIT")
        return {"decision": decision, "idempotent_replay": False}

    def metrics(self) -> dict[str, Any]:
        with self._connection() as connection:
            row = connection.execute(
                """
                SELECT
                    COUNT(*) AS total,
                    SUM(CASE WHEN review_status = 'PENDING' THEN 1 ELSE 0 END) AS pending,
                    SUM(CASE WHEN review_status = 'APPROVED' THEN 1 ELSE 0 END) AS approved,
                    SUM(CASE WHEN review_status = 'REJECTED' THEN 1 ELSE 0 END) AS rejected,
                    SUM(CASE WHEN review_status = 'AUTO_PASS' THEN 1 ELSE 0 END) AS auto_pass,
                    ROUND(AVG(risk_score), 1) AS average_risk_score,
                    SUM(CASE WHEN provenance_complete = 1 THEN 1 ELSE 0 END) AS provenance_complete
                FROM gates
                """
            ).fetchone()
        metrics = dict(row)
        total = metrics["total"] or 0
        metrics["provenance_rate"] = round(
            (metrics.pop("provenance_complete") or 0) * 100 / total, 1
        ) if total else 0
        return metrics
