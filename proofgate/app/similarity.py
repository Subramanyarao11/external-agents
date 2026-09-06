"""Historical comparison through local evidence or Databricks AI Search."""

from __future__ import annotations

import os
import sys
from typing import Any, Protocol


class SimilarChangeFinder(Protocol):
    def find(
        self, gate: dict[str, Any], candidates: list[dict[str, Any]]
    ) -> dict[str, Any]: ...


def _risk_band(score: int) -> str:
    if score >= 70:
        return "high"
    if score >= 30:
        return "medium"
    return "low"


def _query_summary(gate: dict[str, Any]) -> str:
    file_count = gate["changed_file_count"]
    size = "small" if file_count <= 2 else "medium" if file_count <= 6 else "large"
    parts = [
        f"decision={gate['automated_verdict'].lower()}",
        f"risk={_risk_band(gate['risk_score'])}",
        f"change_size={size}",
        f"tests_failed={'yes' if gate['tests_failed'] else 'no'}",
        f"provenance={'complete' if gate['provenance_complete'] else 'incomplete'}",
    ]
    parts.extend(f"reason={reason.lower()}" for reason in gate["reason_codes"])
    return " ".join(parts)


class LocalSimilarityFinder:
    def find(
        self, gate: dict[str, Any], candidates: list[dict[str, Any]]
    ) -> dict[str, Any]:
        gate_reasons = set(gate["reason_codes"])
        results: list[dict[str, Any]] = []
        for candidate in candidates:
            if candidate["event_id"] == gate["event_id"]:
                continue
            candidate_reasons = set(candidate["reason_codes"])
            union = gate_reasons | candidate_reasons
            reason_similarity = len(gate_reasons & candidate_reasons) / len(union) if union else 1.0
            risk_similarity = 1.0 - abs(gate["risk_score"] - candidate["risk_score"]) / 100
            provenance_similarity = float(
                gate["provenance_complete"] == candidate["provenance_complete"]
            )
            score = round(
                0.5 * reason_similarity + 0.35 * risk_similarity + 0.15 * provenance_similarity,
                3,
            )
            results.append(
                {
                    "event_id": candidate["event_id"],
                    "repo_id": candidate["repo_id"],
                    "checkpoint_id": candidate["checkpoint_id"],
                    "occurred_at": candidate["occurred_at"],
                    "decision": candidate["automated_verdict"],
                    "risk_score": candidate["risk_score"],
                    "similarity_score": score,
                }
            )
        results.sort(key=lambda item: item["similarity_score"], reverse=True)
        return {
            "matches": results[:3],
            "query_summary": _query_summary(gate),
            "generated_by": "deterministic-local-preview",
        }


class DatabricksSimilarityFinder:
    COLUMNS = [
        "event_id",
        "repo_id",
        "checkpoint_id",
        "occurred_at",
        "decision",
        "risk_score",
        "source_event_id",
    ]

    def __init__(self, endpoint_name: str, index_name: str):
        from databricks.ai_search.client import AISearchClient

        self.index = AISearchClient().get_index(
            endpoint_name=endpoint_name,
            index_name=index_name,
        )

    def find(
        self, gate: dict[str, Any], candidates: list[dict[str, Any]]
    ) -> dict[str, Any]:
        del candidates
        query_summary = _query_summary(gate)
        response = self.index.similarity_search(
            query_text=query_summary,
            columns=self.COLUMNS,
            filters={"event_id NOT": gate["event_id"]},
            num_results=4,
        )
        manifest = response.get("manifest", {}).get("columns", [])
        names = [column["name"] for column in manifest]
        matches = []
        for values in response.get("result", {}).get("data_array", []):
            row = dict(zip(names, values))
            similarity_score = values[-1] if len(values) > len(names) else row.pop("score", None)
            row.pop("similarity_summary", None)
            row["similarity_score"] = similarity_score
            if row.get("event_id") != gate["event_id"]:
                matches.append(row)
        return {
            "matches": matches[:3],
            "query_summary": query_summary,
            "generated_by": "databricks-ai-search",
        }


def create_similarity_finder() -> SimilarChangeFinder:
    endpoint = os.environ.get("PROOFGATE_SEARCH_ENDPOINT", "").strip()
    index = os.environ.get("PROOFGATE_SEARCH_INDEX", "").strip()
    if endpoint and index:
        try:
            return DatabricksSimilarityFinder(endpoint, index)
        except Exception as error:  # provider auth/config errors must not stop reviews
            print(
                "Databricks AI Search is unavailable; using deterministic local "
                f"similarity ({type(error).__name__}).",
                file=sys.stderr,
            )
    return LocalSimilarityFinder()
