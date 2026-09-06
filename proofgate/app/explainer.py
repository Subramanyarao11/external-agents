"""Advisory explanations for deterministic ProofGate decisions."""

from __future__ import annotations

import json
import os
from typing import Any, Protocol


class GateExplainer(Protocol):
    def explain(self, gate: dict[str, Any]) -> dict[str, Any]: ...


def _safe_evidence(gate: dict[str, Any]) -> dict[str, Any]:
    """Keep prompts inside the same allowlist as the warehouse boundary."""
    return {
        "automated_verdict": gate["automated_verdict"],
        "risk_score": gate["risk_score"],
        "changed_file_count": gate["changed_file_count"],
        "tests_total": gate["tests_total"],
        "tests_failed": gate["tests_failed"],
        "provenance_complete": gate["provenance_complete"],
        "reason_codes": gate["reason_codes"],
        "hard_stop_codes": gate["hard_stop_codes"],
    }


def _content_text(content: Any) -> str:
    """Extract only final text blocks from chat responses, never reasoning blocks."""
    if isinstance(content, str):
        return content.strip()
    if not isinstance(content, (list, tuple)):
        return ""
    parts: list[str] = []
    for block in content:
        block_type = block.get("type") if isinstance(block, dict) else getattr(block, "type", None)
        if str(block_type or "").lower() not in {"text", "output_text"}:
            continue
        text = block.get("text") if isinstance(block, dict) else getattr(block, "text", None)
        if isinstance(text, str) and text.strip():
            parts.append(text.strip())
    return "\n".join(parts)


class DeterministicExplainer:
    """Offline preview used when Databricks Model Serving is unavailable."""

    def explain(self, gate: dict[str, Any]) -> dict[str, Any]:
        evidence = _safe_evidence(gate)
        if evidence["hard_stop_codes"]:
            next_step = "Resolve the hard-stop evidence or record an accountable human exception."
        elif evidence["reason_codes"]:
            next_step = "Review the warning signals before merging."
        else:
            next_step = "No policy exception needs human action."
        reasons = ", ".join(code.replace("_", " ").lower() for code in evidence["reason_codes"])
        explanation = (
            f"ProofGate scored this change {evidence['risk_score']}/100 and returned "
            f"{evidence['automated_verdict'].replace('_', ' ').lower()}. "
            + (f"The recorded signals are {reasons}. " if reasons else "All required evidence is present. ")
            + next_step
        )
        return {
            "explanation": explanation,
            "generated_by": "deterministic-local-preview",
            "authority": "advisory_only",
            "evidence": evidence,
        }


class DatabricksExplainer:
    def __init__(self, endpoint: str):
        from databricks.sdk import WorkspaceClient

        self.endpoint = endpoint
        self.workspace = WorkspaceClient()

    def explain(self, gate: dict[str, Any]) -> dict[str, Any]:
        from databricks.sdk.service.serving import ChatMessage, ChatMessageRole

        evidence = _safe_evidence(gate)
        response = self.workspace.serving_endpoints.query(
            name=self.endpoint,
            messages=[
                ChatMessage(
                    role=ChatMessageRole.SYSTEM,
                    content=(
                        "Explain deterministic software change-gate evidence in at most 80 words. "
                        "State why review is or is not needed and one concrete next step. "
                        "Never invent files, tests, people, or causes. Never change or second-guess "
                        "the supplied verdict. This text is advisory, not a decision."
                    ),
                ),
                ChatMessage(
                    role=ChatMessageRole.USER,
                    content="Safe structured evidence:\n" + json.dumps(evidence, sort_keys=True),
                ),
            ],
            max_tokens=400,
            temperature=0.0,
        )
        if not response.choices or not response.choices[0].message:
            raise RuntimeError("explanation endpoint returned no message")
        explanation = _content_text(response.choices[0].message.content)
        if not explanation:
            fallback = DeterministicExplainer().explain(gate)
            fallback["generated_by"] = "deterministic-fallback"
            return fallback
        return {
            "explanation": explanation,
            "generated_by": self.endpoint,
            "authority": "advisory_only",
            "evidence": evidence,
        }


def create_explainer() -> GateExplainer:
    endpoint = os.environ.get("PROOFGATE_EXPLANATION_MODEL", "").strip()
    return DatabricksExplainer(endpoint) if endpoint else DeterministicExplainer()
