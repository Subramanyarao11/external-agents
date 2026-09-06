from __future__ import annotations

import json
import sys
import tempfile
import threading
import unittest
import urllib.error
import urllib.request
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch


APP_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(APP_DIR))

from app import create_server  # noqa: E402
from decision_sync import DECISION_MERGE, DecisionPublisher  # noqa: E402
from explainer import _content_text  # noqa: E402
from github_dispatch import GitHubWorkflowDispatcher  # noqa: E402
from similarity import LocalSimilarityFinder, create_similarity_finder  # noqa: E402
from store import ReviewConflictError, SQLiteStore, ValidationError  # noqa: E402
from warehouse_sync import WarehouseSynchronizer  # noqa: E402


class RecordingDecisionPublisher:
    def __init__(self) -> None:
        self.calls: list[tuple[dict, dict]] = []

    def publish(self, decision: dict, gate: dict) -> dict[str, str]:
        self.calls.append((decision, gate))
        return {"status": "SYNCED", "statement_id": "statement-review-1"}


class RecordingWorkflowDispatcher:
    def __init__(self) -> None:
        self.calls: list[tuple[dict, dict]] = []

    def dispatch(self, decision: dict, gate: dict) -> dict[str, str]:
        self.calls.append((decision, gate))
        if decision["action"] != "APPROVE":
            return {"status": "SKIPPED_NOT_APPROVED"}
        return {"status": "DISPATCHED", "candidate_ref": gate["commit_sha"]}


class StoreTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary_directory = tempfile.TemporaryDirectory()
        self.store = SQLiteStore(Path(self.temporary_directory.name) / "test.db")
        self.store.seed_demo()

    def tearDown(self) -> None:
        self.temporary_directory.cleanup()

    def test_seed_is_repeatable(self) -> None:
        self.assertEqual(self.store.seed_demo()["inserted"], 0)
        self.assertEqual(len(self.store.list_gates()), 4)
        graph_gate = self.store.get_gate("gate-checkout-retry")
        self.assertEqual(graph_gate["impact_analysis_source"], "entire-graph-v0.4.0")
        self.assertTrue(graph_gate["impact_analysis_complete"])
        self.assertEqual(graph_gate["max_dependent_count"], 24)

    def test_decision_updates_review_without_rewriting_policy_verdict(self) -> None:
        before = self.store.get_gate("gate-checkout-retry")
        result = self.store.decide(
            before["event_id"],
            action="APPROVE",
            actor_id="reviewer@example.test",
            reason="Failure is a known flaky fixture; manual reproduction passed.",
            expected_version=before["version"],
            idempotency_key="approve-checkout-once",
        )
        after = self.store.get_gate(before["event_id"])
        self.assertFalse(result["idempotent_replay"])
        self.assertEqual(after["automated_verdict"], "APPROVAL_REQUIRED")
        self.assertEqual(after["review_status"], "APPROVED")
        self.assertEqual(after["version"], 2)
        self.assertEqual(len(after["decisions"]), 1)

    def test_replayed_idempotency_key_does_not_create_second_decision(self) -> None:
        arguments = {
            "event_id": "gate-auth-refresh",
            "action": "REJECT",
            "actor_id": "security-reviewer",
            "reason": "Attach a complete Entire checkpoint before merge.",
            "expected_version": 1,
            "idempotency_key": "reject-auth-once",
        }
        first = self.store.decide(**arguments)
        second = self.store.decide(**arguments)
        self.assertFalse(first["idempotent_replay"])
        self.assertTrue(second["idempotent_replay"])
        self.assertEqual(first["decision"]["decision_id"], second["decision"]["decision_id"])
        self.assertEqual(len(self.store.get_gate(arguments["event_id"])["decisions"]), 1)

    def test_stale_version_is_rejected(self) -> None:
        self.store.decide(
            "gate-search-empty-state",
            action="APPROVE",
            actor_id="reviewer",
            reason="Evidence reviewed.",
            expected_version=1,
            idempotency_key="first-review",
        )
        with self.assertRaises(ReviewConflictError) as context:
            self.store.decide(
                "gate-search-empty-state",
                action="REJECT",
                actor_id="second-reviewer",
                reason="I opened a stale copy.",
                expected_version=1,
                idempotency_key="stale-review",
            )
        self.assertEqual(context.exception.current_version, 2)

    def test_idempotency_key_cannot_cross_gate_boundary(self) -> None:
        self.store.decide(
            "gate-checkout-retry",
            action="REJECT",
            actor_id="reviewer",
            reason="Required test failed.",
            expected_version=1,
            idempotency_key="globally-unique-key",
        )
        with self.assertRaises(ValidationError):
            self.store.decide(
                "gate-auth-refresh",
                action="REJECT",
                actor_id="reviewer",
                reason="Provenance is incomplete.",
                expected_version=1,
                idempotency_key="globally-unique-key",
            )

    def test_evidence_refresh_preserves_human_review(self) -> None:
        gate = self.store.get_gate("gate-search-empty-state")
        self.store.decide(
            gate["event_id"],
            action="APPROVE",
            actor_id="reviewer",
            reason="Evidence reviewed.",
            expected_version=1,
            idempotency_key="review-before-sync",
        )
        refreshed = {key: value for key, value in gate.items() if key != "decisions"}
        refreshed["risk_score"] = 47
        refreshed["reason_codes"] = ["HISTORICAL_RISK_INCREASED"]
        refreshed["review_status"] = "PENDING"
        self.store.upsert_gates([refreshed])
        after = self.store.get_gate(gate["event_id"])
        self.assertEqual(after["risk_score"], 47)
        self.assertEqual(after["review_status"], "APPROVED")
        self.assertEqual(after["version"], 2)
        self.assertEqual(len(after["decisions"]), 1)

    def test_warehouse_row_mapping_contains_no_raw_content(self) -> None:
        gate = WarehouseSynchronizer._gate(
            {
                "event_id": "event-1",
                "repo_id": "repo-1",
                "checkpoint_id": "checkpoint-1",
                "commit_sha": "abc1234",
                "automated_verdict": "APPROVAL_REQUIRED",
                "risk_score": "91",
                "changed_file_count": "4",
                "impact_analysis_source": "entire-graph-v0.4.0",
                "impact_analysis_complete": "true",
                "max_dependent_count": "17",
                "tests_total": "17",
                "tests_failed": "1",
                "provenance_complete": "true",
                "reason_codes_json": '["REQUIRED_TEST_FAILED"]',
                "hard_stop_codes_json": '["REQUIRED_TEST_FAILED"]',
                "occurred_at": "2026-09-04 12:00:00+00:00",
            }
        )
        self.assertEqual(gate["review_status"], "PENDING")
        self.assertEqual(gate["max_dependent_count"], 17)
        self.assertTrue(gate["impact_analysis_complete"])
        self.assertNotIn("raw_prompt", gate)
        self.assertNotIn("source_code", gate)

    def test_warehouse_sync_waits_for_a_cold_warehouse(self) -> None:
        class FakeExecution:
            def __init__(self) -> None:
                self.polls = 0

            def execute_statement(self, **_kwargs):
                return SimpleNamespace(
                    statement_id="statement-1",
                    status=SimpleNamespace(state=SimpleNamespace(value="PENDING")),
                )

            def get_statement(self, statement_id):
                self.assert_statement_id = statement_id
                self.polls += 1
                return SimpleNamespace(
                    statement_id=statement_id,
                    status=SimpleNamespace(state=SimpleNamespace(value="SUCCEEDED")),
                    manifest=SimpleNamespace(
                        schema=SimpleNamespace(columns=[SimpleNamespace(name="event_id")])
                    ),
                    result=SimpleNamespace(data_array=[["event-1"]]),
                )

        execution = FakeExecution()
        synchronizer = WarehouseSynchronizer(
            "warehouse-1",
            "main",
            "proofgate",
            workspace=SimpleNamespace(statement_execution=execution),
            poll_interval_seconds=0,
        )
        self.assertEqual(synchronizer._query(), [{"event_id": "event-1"}])
        self.assertEqual(execution.polls, 1)
        self.assertEqual(execution.assert_statement_id, "statement-1")

    def test_ai_search_configuration_failure_falls_back_locally(self) -> None:
        environment = {
            "PROOFGATE_SEARCH_ENDPOINT": "proofgate-search",
            "PROOFGATE_SEARCH_INDEX": "main.proofgate.history",
        }
        with patch.dict("os.environ", environment, clear=False), patch(
            "similarity.DatabricksSimilarityFinder",
            side_effect=RuntimeError("credentials unavailable"),
        ):
            finder = create_similarity_finder()
        self.assertIsInstance(finder, LocalSimilarityFinder)

    def test_explanation_extracts_final_text_without_reasoning(self) -> None:
        content = [
            {"type": "reasoning", "summary": [{"text": "private reasoning"}]},
            {"type": "output_text", "text": "Review is required. Verify the hard stop."},
        ]
        self.assertEqual(
            _content_text(content),
            "Review is required. Verify the hard stop.",
        )

    def test_decision_publisher_binds_values_instead_of_interpolating(self) -> None:
        class FakeExecution:
            def __init__(self) -> None:
                self.arguments: dict = {}

            def execute_statement(self, **kwargs):
                self.arguments = kwargs
                return SimpleNamespace(
                    statement_id="statement-1",
                    status=SimpleNamespace(state=SimpleNamespace(value="SUCCEEDED")),
                )

        execution = FakeExecution()
        workspace = SimpleNamespace(statement_execution=execution)
        publisher = DecisionPublisher(
            "warehouse-1",
            "main",
            "proofgate",
            workspace=workspace,
            parameter_factory=lambda **kwargs: kwargs,
        )
        decision = {
            "decision_id": "decision-1",
            "gate_event_id": "gate-'quoted",
            "action": "APPROVE",
            "actor_id": "reviewer@example.test",
            "reason": "I reviewed the bounded evidence.",
            "previous_version": 1,
            "occurred_at": "2026-09-04T12:00:00+00:00",
            "idempotency_key": "review-once",
        }
        receipt = publisher.publish(decision, {"checkpoint_id": "checkpoint-1"})
        self.assertEqual(receipt["status"], "SYNCED")
        self.assertEqual(execution.arguments["statement"], DECISION_MERGE)
        self.assertNotIn(decision["gate_event_id"], DECISION_MERGE)
        parameters = {
            item["name"]: item["value"] for item in execution.arguments["parameters"]
        }
        self.assertEqual(parameters["gate_event_id"], decision["gate_event_id"])
        self.assertEqual(parameters["decision_reason"], decision["reason"])

    def test_github_dispatcher_sends_only_the_exact_candidate_sha(self) -> None:
        class Response:
            status = 204
            headers = {"X-GitHub-Request-Id": "request-1"}

            def __enter__(self):
                return self

            def __exit__(self, *_args):
                return None

        recorded: dict = {}

        def open_request(request, timeout):
            recorded["request"] = request
            recorded["timeout"] = timeout
            return Response()

        dispatcher = GitHubWorkflowDispatcher(
            "entireio/proofgate-demo",
            "proofgate.yml",
            "main",
            "secret-token",
            opener=open_request,
        )
        result = dispatcher.dispatch(
            {"action": "APPROVE"},
            {"commit_sha": "0123456789abcdef0123456789abcdef01234567"},
        )
        request = recorded["request"]
        payload = json.loads(request.data)
        self.assertEqual(result["status"], "DISPATCHED")
        self.assertEqual(recorded["timeout"], 15.0)
        self.assertEqual(payload["ref"], "main")
        self.assertEqual(
            payload["inputs"]["candidate_ref"],
            "0123456789abcdef0123456789abcdef01234567",
        )
        self.assertEqual(request.get_header("Authorization"), "Bearer secret-token")


class HTTPTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary_directory = tempfile.TemporaryDirectory()
        self.publisher = RecordingDecisionPublisher()
        self.dispatcher = RecordingWorkflowDispatcher()
        self.server = create_server(
            "127.0.0.1",
            0,
            Path(self.temporary_directory.name) / "http.db",
            decision_publisher=self.publisher,
            github_dispatcher=self.dispatcher,
        )
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.base_url = f"http://127.0.0.1:{self.server.server_port}"

    def tearDown(self) -> None:
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=2)
        self.temporary_directory.cleanup()

    def request(self, path: str, body: dict | None = None) -> tuple[int, dict]:
        data = None if body is None else json.dumps(body).encode("utf-8")
        request = urllib.request.Request(
            self.base_url + path,
            data=data,
            method="GET" if body is None else "POST",
            headers={"Content-Type": "application/json"},
        )
        try:
            response = urllib.request.urlopen(request, timeout=2)
        except urllib.error.HTTPError as error:
            try:
                return error.code, json.load(error)
            finally:
                error.close()
        with response:
            return response.status, json.load(response)

    def test_health_and_list_are_explicitly_demo_data(self) -> None:
        status, health = self.request("/healthz")
        self.assertEqual(status, 200)
        self.assertEqual(health["mode"], "demo")
        status, result = self.request("/api/gates")
        self.assertEqual(status, 200)
        self.assertEqual(result["data_mode"], "demo")
        self.assertEqual(len(result["gates"]), 4)

    def test_http_decision_and_conflict_contract(self) -> None:
        body = {
            "action": "APPROVE",
            "actor_id": "demo-reviewer",
            "reason": "Reviewed the evidence in the Control Room.",
            "expected_version": 1,
            "idempotency_key": "http-approval",
        }
        status, result = self.request("/api/gates/gate-checkout-retry/decision", body)
        self.assertEqual(status, 200)
        self.assertFalse(result["idempotent_replay"])
        self.assertEqual(result["decision_sync"]["status"], "SYNCED")
        self.assertEqual(result["workflow_dispatch"]["status"], "DISPATCHED")
        self.assertEqual(len(self.publisher.calls), 1)
        self.assertEqual(len(self.dispatcher.calls), 1)
        body["idempotency_key"] = "http-stale-second-request"
        status, conflict = self.request("/api/gates/gate-checkout-retry/decision", body)
        self.assertEqual(status, 409)
        self.assertEqual(conflict["current_version"], 2)

    def test_local_explanation_is_advisory_and_allowlisted(self) -> None:
        status, result = self.request("/api/gates/gate-auth-refresh/explain", {})
        self.assertEqual(status, 200)
        self.assertEqual(result["authority"], "advisory_only")
        self.assertEqual(result["generated_by"], "deterministic-local-preview")
        self.assertNotIn("summary", result["evidence"])
        self.assertNotIn("checkpoint_id", result["evidence"])
        self.assertIn("PROVENANCE_INCOMPLETE", result["evidence"]["reason_codes"])

    def test_local_similarity_excludes_the_current_gate(self) -> None:
        status, result = self.request("/api/gates/gate-auth-refresh/similar", {})
        self.assertEqual(status, 200)
        self.assertEqual(result["generated_by"], "deterministic-local-preview")
        self.assertTrue(result["query_summary"])
        self.assertNotIn(
            "gate-auth-refresh", {match["event_id"] for match in result["matches"]}
        )
        scores = [match["similarity_score"] for match in result["matches"]]
        self.assertEqual(scores, sorted(scores, reverse=True))


if __name__ == "__main__":
    unittest.main()
