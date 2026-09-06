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
              gold.event_id,
              gold.repo_id,
              gold.checkpoint_id,
              coalesce(gold.commit_sha, '') AS commit_sha,
              gold.decision AS automated_verdict,
              gold.risk_score,
              gold.changed_file_count,
              coalesce(gold.impact_analysis_source, 'git-path-heuristic') AS impact_analysis_source,
              coalesce(gold.impact_analysis_complete, false) AS impact_analysis_complete,
              coalesce(gold.max_dependent_count, 0) AS max_dependent_count,
              gold.test_total AS tests_total,
              gold.test_failed AS tests_failed,
              gold.provenance_complete,
              to_json(gold.reason_codes) AS reason_codes_json,
              to_json(gold.hard_stop_codes) AS hard_stop_codes_json,
              gold.policy_version,
              coalesce(gold.agent_family, 'unknown') AS agent_family,
              coalesce(gold.model_family, 'unknown') AS model_family,
              coalesce(gold.changed_line_count, 0) AS changed_line_count,
              coalesce(gold.impacted_entity_count, 0) AS impacted_entity_count,
              coalesce(gold.dependency_depth, 0) AS dependency_depth,
              coalesce(to_json(gold.sensitive_components), '[]') AS sensitive_components_json,
              coalesce(gold.required_test_missing, false) AS required_test_missing,
              coalesce(gold.similar_change_count, 0) AS similar_change_count,
              coalesce(gold.similar_failure_rate, 0.0) AS similar_failure_rate,
              coalesce(gold.component_failure_rate, 0.0) AS component_failure_rate,
              CAST(gold.history_snapshot_at AS STRING) AS history_snapshot_at,
              CAST(gold.feature_generated_at AS STRING) AS feature_generated_at,
              coalesce(silver.source_adapter, 'unknown') AS source_adapter,
              coalesce(silver.schema_version, 'unknown') AS schema_version,
              coalesce(silver.redaction_version, 'unknown') AS redaction_version,
              coalesce(silver.dropped_field_count, 0) AS dropped_field_count,
              coalesce(silver.payload_hash, '') AS payload_hash,
              coalesce(CAST(get_json_object(silver.payload_json, '$.session_count') AS INT), 0) AS session_count,
              coalesce(CAST(get_json_object(silver.payload_json, '$.handoff_count') AS INT), 0) AS handoff_count,
              coalesce(get_json_object(silver.payload_json, '$.tool_categories'), '[]') AS tool_categories_json,
              coalesce(CAST(get_json_object(silver.payload_json, '$.history_available') AS BOOLEAN), false) AS history_available,
              coalesce(CAST(get_json_object(silver.payload_json, '$.baseline_change_count') AS INT), 0) AS baseline_change_count,
              coalesce(get_json_object(silver.payload_json, '$.passport_fingerprint'), '') AS passport_fingerprint,
              coalesce(get_json_object(silver.payload_json, '$.safe_similarity_summary'), '') AS safe_similarity_summary,
              CAST(gold.occurred_at AS STRING) AS occurred_at
            FROM `{self.catalog}`.`{self.schema}`.`gold_change_risk_features` AS gold
            LEFT JOIN `{self.catalog}`.`{self.schema}`.`silver_checkpoint_events` AS silver
              ON silver.event_id = gold.event_id
            ORDER BY gold.occurred_at DESC
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
        sensitive_components = json.loads(row.get("sensitive_components_json") or "[]")
        tool_categories = json.loads(row.get("tool_categories_json") or "[]")
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
            "evidence": {
                "policy_version": row.get("policy_version") or "unknown",
                "source_adapter": row.get("source_adapter") or "unknown",
                "agent_family": row.get("agent_family") or "unknown",
                "model_family": row.get("model_family") or "unknown",
                "session_count": int(row.get("session_count") or 0),
                "handoff_count": int(row.get("handoff_count") or 0),
                "tool_categories": tool_categories,
                "changed_line_count": int(row.get("changed_line_count") or 0),
                "impacted_entity_count": int(row.get("impacted_entity_count") or 0),
                "dependency_depth": int(row.get("dependency_depth") or 0),
                "sensitive_components": sensitive_components,
                "required_test_missing": str(row.get("required_test_missing", "false")).lower() == "true",
                "history_available": str(row.get("history_available", "false")).lower() == "true",
                "baseline_change_count": int(row.get("baseline_change_count") or 0),
                "similar_change_count": int(row.get("similar_change_count") or 0),
                "similar_failure_rate": float(row.get("similar_failure_rate") or 0),
                "component_failure_rate": float(row.get("component_failure_rate") or 0),
                "history_snapshot_at": row.get("history_snapshot_at") or "",
                "schema_version": row.get("schema_version") or "unknown",
                "redaction_version": row.get("redaction_version") or "unknown",
                "dropped_field_count": int(row.get("dropped_field_count") or 0),
                "payload_hash": row.get("payload_hash") or "",
                "passport_fingerprint": row.get("passport_fingerprint") or "",
                "safe_similarity_summary": row.get("safe_similarity_summary") or "",
                "feature_generated_at": row.get("feature_generated_at") or "",
            },
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
