"""Lakebase-backed ProofGate store for Databricks Apps.

Imports are intentionally lazy so local SQLite development needs no third-party
packages. Databricks Apps supplies the workspace identity and PG* connection
details; each new pooled connection mints a fresh short-lived database token.
"""

from __future__ import annotations

import json
import os
import uuid
from contextlib import contextmanager
from datetime import datetime, timezone
from typing import Any, Iterator

from store import GateNotFoundError, ReviewConflictError, SEED_GATES, ValidationError


def _now() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="seconds")


class LakebaseStore:
    def __init__(self) -> None:
        required = ("PGHOST", "PGDATABASE", "PGUSER", "PROOFGATE_LAKEBASE_ENDPOINT")
        missing = [name for name in required if not os.environ.get(name)]
        if missing:
            raise RuntimeError(f"missing Lakebase environment variables: {', '.join(missing)}")

        try:
            import psycopg
            from psycopg.rows import dict_row
            from psycopg_pool import ConnectionPool
            from databricks.sdk import WorkspaceClient
        except ImportError as error:
            raise RuntimeError(
                "Lakebase mode requires psycopg[binary,pool] and databricks-sdk"
            ) from error

        workspace = WorkspaceClient()
        endpoint = os.environ["PROOFGATE_LAKEBASE_ENDPOINT"]

        class OAuthConnection(psycopg.Connection):
            @classmethod
            def connect(cls, conninfo: str = "", **kwargs: Any):
                credential = workspace.postgres.generate_database_credential(
                    endpoint=endpoint
                )
                kwargs["password"] = credential.token
                return super().connect(conninfo, **kwargs)

        conninfo = " ".join(
            (
                f"dbname={os.environ['PGDATABASE']}",
                f"user={os.environ['PGUSER']}",
                f"host={os.environ['PGHOST']}",
                f"port={os.environ.get('PGPORT', '5432')}",
                f"sslmode={os.environ.get('PGSSLMODE', 'require')}",
                f"application_name={os.environ.get('PGAPPNAME', 'proofgate')}",
            )
        )
        self._pool = ConnectionPool(
            conninfo=conninfo,
            connection_class=OAuthConnection,
            kwargs={"row_factory": dict_row},
            min_size=1,
            max_size=6,
            open=True,
        )
        self.initialize()

    @contextmanager
    def _connection(self) -> Iterator[Any]:
        with self._pool.connection() as connection:
            yield connection

    def close(self) -> None:
        self._pool.close()

    def initialize(self) -> None:
        with self._connection() as connection, connection.cursor() as cursor:
            cursor.execute(
                """
                CREATE TABLE IF NOT EXISTS proofgate_gates (
                    event_id TEXT PRIMARY KEY,
                    repo_id TEXT NOT NULL,
                    checkpoint_id TEXT NOT NULL,
                    commit_sha TEXT NOT NULL,
                    automated_verdict TEXT NOT NULL,
                    risk_score INTEGER NOT NULL CHECK (risk_score BETWEEN 0 AND 100),
                    changed_file_count INTEGER NOT NULL,
                    tests_total INTEGER NOT NULL,
                    tests_failed INTEGER NOT NULL,
                    provenance_complete BOOLEAN NOT NULL,
                    summary TEXT NOT NULL,
                    reason_codes_json JSONB NOT NULL,
                    hard_stop_codes_json JSONB NOT NULL,
                    review_status TEXT NOT NULL,
                    version INTEGER NOT NULL DEFAULT 1,
                    occurred_at TIMESTAMPTZ NOT NULL,
                    updated_at TIMESTAMPTZ NOT NULL
                )
                """
            )
            cursor.execute(
                """
                CREATE TABLE IF NOT EXISTS proofgate_decisions (
                    decision_id UUID PRIMARY KEY,
                    gate_event_id TEXT NOT NULL REFERENCES proofgate_gates(event_id),
                    action TEXT NOT NULL CHECK (action IN ('APPROVE', 'REJECT')),
                    actor_id TEXT NOT NULL,
                    reason TEXT NOT NULL,
                    previous_version INTEGER NOT NULL,
                    resulting_version INTEGER NOT NULL,
                    idempotency_key TEXT NOT NULL UNIQUE,
                    occurred_at TIMESTAMPTZ NOT NULL
                )
                """
            )
            cursor.execute(
                """
                CREATE INDEX IF NOT EXISTS proofgate_decisions_gate_event_id_idx
                ON proofgate_decisions(gate_event_id, occurred_at DESC)
                """
            )

    def seed_demo(self, *, reset: bool = False) -> dict[str, int]:
        """Seed only when explicitly enabled for a deployed hackathon demo."""
        with self._connection() as connection, connection.cursor() as cursor:
            if reset:
                cursor.execute("DELETE FROM proofgate_decisions")
                cursor.execute("DELETE FROM proofgate_gates")
            inserted = 0
            for gate in SEED_GATES:
                cursor.execute(
                    """
                    INSERT INTO proofgate_gates (
                        event_id, repo_id, checkpoint_id, commit_sha,
                        automated_verdict, risk_score, changed_file_count,
                        tests_total, tests_failed, provenance_complete, summary,
                        reason_codes_json, hard_stop_codes_json, review_status,
                        version, occurred_at, updated_at
                    ) VALUES (
                        %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s,
                        %s::jsonb, %s::jsonb, %s, 1, %s, %s
                    ) ON CONFLICT (event_id) DO NOTHING
                    """,
                    (
                        gate["event_id"], gate["repo_id"], gate["checkpoint_id"],
                        gate["commit_sha"], gate["automated_verdict"], gate["risk_score"],
                        gate["changed_file_count"], gate["tests_total"], gate["tests_failed"],
                        gate["provenance_complete"], gate["summary"],
                        json.dumps(gate["reason_codes"]),
                        json.dumps(gate["hard_stop_codes"]), gate["review_status"],
                        gate["occurred_at"], _now(),
                    ),
                )
                inserted += cursor.rowcount
        return {"inserted": inserted, "total": len(SEED_GATES)}

    def upsert_gates(self, gates: list[dict[str, Any]]) -> dict[str, int]:
        """Refresh machine evidence while preserving review state and history."""
        with self._connection() as connection, connection.cursor() as cursor:
            for gate in gates:
                cursor.execute(
                    """
                    INSERT INTO proofgate_gates (
                        event_id, repo_id, checkpoint_id, commit_sha,
                        automated_verdict, risk_score, changed_file_count,
                        tests_total, tests_failed, provenance_complete, summary,
                        reason_codes_json, hard_stop_codes_json, review_status,
                        version, occurred_at, updated_at
                    ) VALUES (
                        %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s,
                        %s::jsonb, %s::jsonb, %s, 1, %s, %s
                    ) ON CONFLICT (event_id) DO UPDATE SET
                        repo_id = EXCLUDED.repo_id,
                        checkpoint_id = EXCLUDED.checkpoint_id,
                        commit_sha = EXCLUDED.commit_sha,
                        automated_verdict = EXCLUDED.automated_verdict,
                        risk_score = EXCLUDED.risk_score,
                        changed_file_count = EXCLUDED.changed_file_count,
                        tests_total = EXCLUDED.tests_total,
                        tests_failed = EXCLUDED.tests_failed,
                        provenance_complete = EXCLUDED.provenance_complete,
                        summary = EXCLUDED.summary,
                        reason_codes_json = EXCLUDED.reason_codes_json,
                        hard_stop_codes_json = EXCLUDED.hard_stop_codes_json,
                        occurred_at = EXCLUDED.occurred_at,
                        updated_at = EXCLUDED.updated_at
                    """,
                    (
                        gate["event_id"], gate["repo_id"], gate["checkpoint_id"],
                        gate["commit_sha"], gate["automated_verdict"], gate["risk_score"],
                        gate["changed_file_count"], gate["tests_total"], gate["tests_failed"],
                        gate["provenance_complete"], gate["summary"],
                        json.dumps(gate["reason_codes"]),
                        json.dumps(gate["hard_stop_codes"]),
                        gate.get("review_status", "PENDING"), gate["occurred_at"], _now(),
                    ),
                )
        return {"upserted": len(gates)}

    @staticmethod
    def _gate(row: dict[str, Any]) -> dict[str, Any]:
        gate = dict(row)
        for key in ("occurred_at", "updated_at"):
            if hasattr(gate[key], "isoformat"):
                gate[key] = gate[key].isoformat()
        for key in ("reason_codes_json", "hard_stop_codes_json"):
            value = gate.pop(key)
            gate[key.removesuffix("_json")] = json.loads(value) if isinstance(value, str) else value
        return gate

    @staticmethod
    def _decision(row: dict[str, Any]) -> dict[str, Any]:
        decision = dict(row)
        decision["decision_id"] = str(decision["decision_id"])
        if hasattr(decision["occurred_at"], "isoformat"):
            decision["occurred_at"] = decision["occurred_at"].isoformat()
        return decision

    def list_gates(self, status: str | None = None) -> list[dict[str, Any]]:
        query = "SELECT * FROM proofgate_gates"
        parameters: tuple[Any, ...] = ()
        if status:
            query += " WHERE review_status = %s"
            parameters = (status,)
        query += " ORDER BY occurred_at DESC"
        with self._connection() as connection, connection.cursor() as cursor:
            cursor.execute(query, parameters)
            return [self._gate(row) for row in cursor.fetchall()]

    def get_gate(self, event_id: str) -> dict[str, Any]:
        with self._connection() as connection, connection.cursor() as cursor:
            cursor.execute("SELECT * FROM proofgate_gates WHERE event_id = %s", (event_id,))
            row = cursor.fetchone()
            if row is None:
                raise GateNotFoundError(event_id)
            cursor.execute(
                """
                SELECT * FROM proofgate_decisions
                WHERE gate_event_id = %s ORDER BY occurred_at DESC
                """,
                (event_id,),
            )
            decisions = cursor.fetchall()
        gate = self._gate(row)
        gate["decisions"] = [self._decision(item) for item in decisions]
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

        with self._connection() as connection, connection.cursor() as cursor:
            cursor.execute(
                "SELECT * FROM proofgate_decisions WHERE idempotency_key = %s",
                (idempotency_key,),
            )
            existing = cursor.fetchone()
            if existing is not None:
                decision = self._decision(existing)
                if decision["gate_event_id"] != event_id:
                    raise ValidationError("idempotency_key was already used for a different gate")
                return {"decision": decision, "idempotent_replay": True}

            cursor.execute(
                "SELECT version FROM proofgate_gates WHERE event_id = %s FOR UPDATE",
                (event_id,),
            )
            row = cursor.fetchone()
            if row is None:
                raise GateNotFoundError(event_id)
            if row["version"] != expected_version:
                raise ReviewConflictError(int(row["version"]))

            decision = {
                "decision_id": str(uuid.uuid4()),
                "gate_event_id": event_id,
                "action": action,
                "actor_id": actor_id,
                "reason": reason,
                "previous_version": expected_version,
                "resulting_version": expected_version + 1,
                "idempotency_key": idempotency_key,
                "occurred_at": _now(),
            }
            cursor.execute(
                """
                INSERT INTO proofgate_decisions (
                    decision_id, gate_event_id, action, actor_id, reason,
                    previous_version, resulting_version, idempotency_key, occurred_at
                ) VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s)
                """,
                tuple(decision.values()),
            )
            cursor.execute(
                """
                UPDATE proofgate_gates
                SET review_status = %s, version = %s, updated_at = %s
                WHERE event_id = %s AND version = %s
                """,
                (
                    "APPROVED" if action == "APPROVE" else "REJECTED",
                    expected_version + 1,
                    decision["occurred_at"],
                    event_id,
                    expected_version,
                ),
            )
        return {"decision": decision, "idempotent_replay": False}

    def metrics(self) -> dict[str, Any]:
        with self._connection() as connection, connection.cursor() as cursor:
            cursor.execute(
                """
                SELECT
                    COUNT(*) AS total,
                    COUNT(*) FILTER (WHERE review_status = 'PENDING') AS pending,
                    COUNT(*) FILTER (WHERE review_status = 'APPROVED') AS approved,
                    COUNT(*) FILTER (WHERE review_status = 'REJECTED') AS rejected,
                    COUNT(*) FILTER (WHERE review_status = 'AUTO_PASS') AS auto_pass,
                    ROUND(AVG(risk_score), 1) AS average_risk_score,
                    COUNT(*) FILTER (WHERE provenance_complete) AS provenance_complete
                FROM proofgate_gates
                """
            )
            metrics = dict(cursor.fetchone())
        total = metrics["total"] or 0
        metrics["average_risk_score"] = float(metrics["average_risk_score"] or 0)
        metrics["provenance_rate"] = round(
            (metrics.pop("provenance_complete") or 0) * 100 / total, 1
        ) if total else 0
        return metrics
