"""Publish human review receipts to the governed Databricks decision table."""

from __future__ import annotations

import os
import time
from typing import Any, Callable

from warehouse_sync import IDENTIFIER


DECISION_MERGE = """
MERGE INTO policy_decision_events AS target
USING (SELECT
  :decision_event_id AS decision_event_id,
  :gate_event_id AS gate_event_id,
  :checkpoint_id AS checkpoint_id,
  :action AS action,
  :actor_id AS actor_id,
  CAST(:expected_version AS INT) AS expected_version,
  :decision_reason AS decision_reason,
  :policy_version AS policy_version,
  CAST(:occurred_at AS TIMESTAMP) AS occurred_at,
  :idempotency_key AS idempotency_key
) AS source
ON target.idempotency_key = source.idempotency_key
WHEN NOT MATCHED THEN INSERT *
""".strip()


class DecisionPublisher:
    def __init__(
        self,
        warehouse_id: str,
        catalog: str,
        schema: str,
        policy_version: str = "proofgate-default-v1",
        *,
        workspace: Any | None = None,
        parameter_factory: Callable[..., Any] | None = None,
    ) -> None:
        if not warehouse_id.strip():
            raise ValueError("warehouse_id is required")
        for label, value in (("catalog", catalog), ("schema", schema)):
            if not IDENTIFIER.fullmatch(value):
                raise ValueError(f"unsafe {label} identifier")
        if not policy_version.strip() or len(policy_version) > 120:
            raise ValueError("policy_version is required and must be at most 120 characters")

        if workspace is None or parameter_factory is None:
            from databricks.sdk import WorkspaceClient
            from databricks.sdk.service.sql import StatementParameterListItem

            workspace = workspace or WorkspaceClient()
            parameter_factory = parameter_factory or StatementParameterListItem

        self.workspace = workspace
        self.parameter_factory = parameter_factory
        self.warehouse_id = warehouse_id
        self.catalog = catalog
        self.schema = schema
        self.policy_version = policy_version

    def publish(self, decision: dict[str, Any], gate: dict[str, Any]) -> dict[str, str]:
        values = {
            "decision_event_id": str(decision["decision_id"]),
            "gate_event_id": str(decision["gate_event_id"]),
            "checkpoint_id": str(gate["checkpoint_id"]),
            "action": str(decision["action"]),
            "actor_id": str(decision["actor_id"]),
            "expected_version": str(decision["previous_version"]),
            "decision_reason": str(decision["reason"]),
            "policy_version": self.policy_version,
            "occurred_at": str(decision["occurred_at"]),
            "idempotency_key": str(decision["idempotency_key"]),
        }
        parameters = [
            self.parameter_factory(
                name=name,
                value=value,
                type="INT" if name == "expected_version" else "STRING",
            )
            for name, value in values.items()
        ]
        response = self.workspace.statement_execution.execute_statement(
            warehouse_id=self.warehouse_id,
            catalog=self.catalog,
            schema=self.schema,
            statement=DECISION_MERGE,
            parameters=parameters,
            wait_timeout="30s",
        )
        deadline = time.monotonic() + 45
        while self._state(response) in {"PENDING", "RUNNING"}:
            if time.monotonic() >= deadline:
                raise RuntimeError("decision publish timed out")
            time.sleep(0.5)
            response = self.workspace.statement_execution.get_statement(response.statement_id)
        state = self._state(response)
        if state != "SUCCEEDED":
            raise RuntimeError(f"decision publish did not succeed: {state}")
        return {"status": "SYNCED", "statement_id": str(response.statement_id)}

    @staticmethod
    def _state(response: Any) -> str:
        state = getattr(getattr(response, "status", None), "state", None)
        return str(getattr(state, "value", state) or "UNKNOWN")


def create_decision_publisher() -> DecisionPublisher | None:
    warehouse_id = os.environ.get("PROOFGATE_WAREHOUSE_ID", "").strip()
    if not warehouse_id:
        return None
    return DecisionPublisher(
        warehouse_id=warehouse_id,
        catalog=os.environ.get("PROOFGATE_CATALOG", "main"),
        schema=os.environ.get("PROOFGATE_SCHEMA", "proofgate_dev"),
        policy_version=os.environ.get("PROOFGATE_POLICY_VERSION", "proofgate-default-v1"),
    )
