from __future__ import annotations

import json
import sys
import tempfile
import threading
import unittest
import urllib.error
import urllib.request
from pathlib import Path


APP_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(APP_DIR))

from app import create_server  # noqa: E402
from store import ReviewConflictError, SQLiteStore, ValidationError  # noqa: E402


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


class HTTPTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary_directory = tempfile.TemporaryDirectory()
        self.server = create_server(
            "127.0.0.1", 0, Path(self.temporary_directory.name) / "http.db"
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
        self.assertEqual(health["mode"], "local-demo")
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
        body["idempotency_key"] = "http-stale-second-request"
        status, conflict = self.request("/api/gates/gate-checkout-retry/decision", body)
        self.assertEqual(status, 409)
        self.assertEqual(conflict["current_version"], 2)


if __name__ == "__main__":
    unittest.main()
