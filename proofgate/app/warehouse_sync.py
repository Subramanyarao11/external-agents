"""Import allowlisted gold features from Databricks SQL into review storage."""

from __future__ import annotations

import json
import os
import re
import time
from typing import Any, Protocol


IDENTIFIER = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*$")


class GateSink(Protocol):
    def upsert_gates(self, gates: list[dict[str, Any]]) -> dict[str, int]: ...


class WarehouseSynchronizer:
    def __init__(
        self,
        warehouse_id: str,
        catalog: str,
        schema: str,
        *,
        workspace: Any | None = None,
        poll_timeout_seconds: float = 90,
        poll_interval_seconds: float = 0.5,
    ):
        if not warehouse_id.strip():
            raise ValueError("warehouse_id is required")
        for label, value in (("catalog", catalog), ("schema", schema)):
            if not IDENTIFIER.fullmatch(value):
                raise ValueError(f"unsafe {label} identifier")
        if workspace is None:
            from databricks.sdk import WorkspaceClient

            workspace = WorkspaceClient()
        self.workspace = workspace
        self.warehouse_id = warehouse_id
        self.catalog = catalog
        self.schema = schema
        self.poll_timeout_seconds = poll_timeout_seconds
        self.poll_interval_seconds = poll_interval_seconds

    @staticmethod
    def _state(response: Any) -> str:
        state = getattr(getattr(response, "status", None), "state", None)
        return str(getattr(state, "value", state) or "UNKNOWN")

    def _query(self) -> list[dict[str, Any]]:
        statement = f"""
            SELECT
              event_id,
              repo_id,
              checkpoint_id,
              coalesce(commit_sha, '') AS commit_sha,
              decision AS automated_verdict,
              risk_score,
              changed_file_count,
              coalesce(impact_analysis_source, 'git-path-heuristic') AS impact_analysis_source,
              coalesce(impact_analysis_complete, false) AS impact_analysis_complete,
              coalesce(max_dependent_count, 0) AS max_dependent_count,
              test_total AS tests_total,
              test_failed AS tests_failed,
              provenance_complete,
              to_json(reason_codes) AS reason_codes_json,
              to_json(hard_stop_codes) AS hard_stop_codes_json,
              CAST(occurred_at AS STRING) AS occurred_at
            FROM `{self.catalog}`.`{self.schema}`.`gold_change_risk_features`
            ORDER BY occurred_at DESC
            LIMIT 500
        """
        response = self.workspace.statement_execution.execute_statement(
            warehouse_id=self.warehouse_id,
            statement=statement,
            wait_timeout="30s",
            row_limit=500,
        )
        deadline = time.monotonic() + self.poll_timeout_seconds
        while self._state(response) in {"PENDING", "RUNNING"}:
            if time.monotonic() >= deadline:
                raise RuntimeError("warehouse evidence query timed out")
            if not response.statement_id:
                raise RuntimeError("warehouse evidence query returned no statement id")
            time.sleep(self.poll_interval_seconds)
            response = self.workspace.statement_execution.get_statement(response.statement_id)
        state = self._state(response)
        if state != "SUCCEEDED":
            status = getattr(response, "status", None)
            error = getattr(status, "error", None)
            detail = getattr(error, "message", None) or str(error or "no error detail")
            raise RuntimeError(f"warehouse statement did not succeed: {state}: {detail}")
        if not response.manifest or not response.manifest.schema or not response.result:
            return []
        names = [column.name for column in response.manifest.schema.columns or []]
        return [dict(zip(names, values)) for values in response.result.data_array or []]

    @staticmethod
    def _gate(row: dict[str, Any]) -> dict[str, Any]:
        verdict = row["automated_verdict"]
        risk_score = int(row["risk_score"])
        tests_failed = int(row["tests_failed"])
        provenance_complete = str(row["provenance_complete"]).lower() == "true"
        impact_analysis_complete = (
            str(row.get("impact_analysis_complete", "false")).lower() == "true"
        )
        reason_codes = json.loads(row["reason_codes_json"] or "[]")
        hard_stop_codes = json.loads(row["hard_stop_codes_json"] or "[]")
        if hard_stop_codes:
            summary = "Policy hard-stop requires accountable human review."
        elif verdict == "WARN":
            summary = "Policy signals recommend review before merge."
        else:
            summary = "Required provenance and test evidence satisfy policy."
        return {
            "event_id": row["event_id"],
            "repo_id": row["repo_id"],
            "checkpoint_id": row["checkpoint_id"],
            "commit_sha": row["commit_sha"],
            "automated_verdict": verdict,
            "risk_score": risk_score,
            "changed_file_count": int(row["changed_file_count"]),
            "impact_analysis_source": row.get(
                "impact_analysis_source", "git-path-heuristic"
            ),
            "impact_analysis_complete": impact_analysis_complete,
            "max_dependent_count": int(row.get("max_dependent_count", 0)),
            "tests_total": int(row["tests_total"]),
            "tests_failed": tests_failed,
            "provenance_complete": provenance_complete,
            "summary": summary,
            "reason_codes": reason_codes,
            "hard_stop_codes": hard_stop_codes,
            "review_status": "AUTO_PASS" if verdict == "PASS" else "PENDING",
            "occurred_at": row["occurred_at"],
        }

    def sync(self, sink: GateSink) -> dict[str, Any]:
        rows = self._query()
        gates = [self._gate(row) for row in rows]
        result = sink.upsert_gates(gates)
        return {**result, "source_rows": len(rows), "source": "databricks-sql"}


def create_synchronizer() -> WarehouseSynchronizer | None:
    warehouse_id = os.environ.get("PROOFGATE_WAREHOUSE_ID", "").strip()
    if not warehouse_id:
        return None
    return WarehouseSynchronizer(
        warehouse_id=warehouse_id,
        catalog=os.environ.get("PROOFGATE_CATALOG", "main"),
        schema=os.environ.get("PROOFGATE_SCHEMA", "proofgate_dev"),
    )
