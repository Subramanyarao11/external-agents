#!/usr/bin/env python3
"""ProofGate Control Room HTTP service.

Run locally with ``python app.py``.  The service uses SQLite demo storage unless
another store is supplied by the deployment entry point.
"""

from __future__ import annotations

import json
import os
import re
from http import HTTPStatus
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any, Protocol
from urllib.parse import unquote, urlparse

from decision_sync import DecisionPublisher, create_decision_publisher
from explainer import GateExplainer, create_explainer
from github_dispatch import GitHubWorkflowDispatcher, create_github_dispatcher
from similarity import SimilarChangeFinder, create_similarity_finder
from store import GateNotFoundError, ReviewConflictError, SQLiteStore, ValidationError
from warehouse_sync import WarehouseSynchronizer, create_synchronizer


APP_DIR = Path(__file__).resolve().parent
STATIC_DIR = APP_DIR / "static"
GATE_PATH = re.compile(r"^/api/gates/([^/]+)$")
DECISION_PATH = re.compile(r"^/api/gates/([^/]+)/decision$")
EXPLAIN_PATH = re.compile(r"^/api/gates/([^/]+)/explain$")
SIMILAR_PATH = re.compile(r"^/api/gates/([^/]+)/similar$")


class GateStore(Protocol):
    def seed_demo(self, *, reset: bool = False) -> dict[str, int]: ...
    def list_gates(self, status: str | None = None) -> list[dict[str, Any]]: ...
    def get_gate(self, event_id: str) -> dict[str, Any]: ...
    def decide(self, event_id: str, **kwargs: Any) -> dict[str, Any]: ...
    def metrics(self) -> dict[str, Any]: ...
    def upsert_gates(self, gates: list[dict[str, Any]]) -> dict[str, int]: ...


class ProofGateHandler(SimpleHTTPRequestHandler):
    server_version = "ProofGate/0.1"

    def __init__(self, *args: Any, **kwargs: Any):
        super().__init__(*args, directory=str(STATIC_DIR), **kwargs)

    @property
    def store(self) -> GateStore:
        return self.server.store  # type: ignore[attr-defined]

    @property
    def data_mode(self) -> str:
        return self.server.data_mode  # type: ignore[attr-defined]

    @property
    def explainer(self) -> GateExplainer:
        return self.server.explainer  # type: ignore[attr-defined]

    @property
    def similarity_finder(self) -> SimilarChangeFinder:
        return self.server.similarity_finder  # type: ignore[attr-defined]

    @property
    def synchronizer(self) -> WarehouseSynchronizer | None:
        return self.server.synchronizer  # type: ignore[attr-defined]

    @property
    def decision_publisher(self) -> DecisionPublisher | None:
        return self.server.decision_publisher  # type: ignore[attr-defined]

    @property
    def github_dispatcher(self) -> GitHubWorkflowDispatcher | None:
        return self.server.github_dispatcher  # type: ignore[attr-defined]

    def _json(self, status: HTTPStatus, payload: dict[str, Any]) -> None:
        body = json.dumps(payload, separators=(",", ":")).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-store")
        self.send_header("X-Content-Type-Options", "nosniff")
        self.end_headers()
        self.wfile.write(body)

    def _body(self) -> dict[str, Any]:
        try:
            size = int(self.headers.get("Content-Length", "0"))
        except ValueError as error:
            raise ValidationError("invalid Content-Length") from error
        if size <= 0 or size > 16_384:
            raise ValidationError("request body must be between 1 byte and 16 KiB")
        try:
            value = json.loads(self.rfile.read(size))
        except (json.JSONDecodeError, UnicodeDecodeError) as error:
            raise ValidationError("request body must be valid JSON") from error
        if not isinstance(value, dict):
            raise ValidationError("request body must be a JSON object")
        return value

    def do_GET(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler API
        path = urlparse(self.path).path
        if path == "/healthz":
            self._json(HTTPStatus.OK, {"status": "ok", "mode": self.data_mode})
            return
        if path == "/api/gates":
            self._json(HTTPStatus.OK, {"gates": self.store.list_gates(), "data_mode": self.data_mode})
            return
        if path == "/api/metrics":
            self._json(HTTPStatus.OK, {"metrics": self.store.metrics(), "data_mode": self.data_mode})
            return
        match = GATE_PATH.match(path)
        if match:
            try:
                gate = self.store.get_gate(unquote(match.group(1)))
            except GateNotFoundError:
                self._json(HTTPStatus.NOT_FOUND, {"error": "gate_not_found"})
                return
            self._json(HTTPStatus.OK, {"gate": gate, "data_mode": self.data_mode})
            return
        if path == "/":
            self.path = "/index.html"
        super().do_GET()

    def do_POST(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler API
        path = urlparse(self.path).path
        try:
            if path == "/api/demo/reset":
                if self.data_mode != "demo":
                    self._json(HTTPStatus.FORBIDDEN, {"error": "demo_reset_disabled"})
                    return
                self._body()
                result = self.store.seed_demo(reset=True)
                self._json(HTTPStatus.OK, {"reset": True, **result})
                return
            if path == "/api/admin/sync":
                self._body()
                if self.synchronizer is None:
                    self._json(
                        HTTPStatus.SERVICE_UNAVAILABLE,
                        {"error": "warehouse_sync_not_configured"},
                    )
                    return
                try:
                    result = self.synchronizer.sync(self.store)
                except Exception as error:  # SQL SDK exposes provider-specific errors
                    self.log_error("warehouse sync failed: %s", type(error).__name__)
                    self._json(
                        HTTPStatus.BAD_GATEWAY,
                        {"error": "warehouse_sync_failed", "message": "The governed evidence import failed."},
                    )
                    return
                self._json(HTTPStatus.OK, result)
                return
            explain_match = EXPLAIN_PATH.match(path)
            if explain_match:
                self._body()
                gate = self.store.get_gate(unquote(explain_match.group(1)))
                try:
                    explanation = self.explainer.explain(gate)
                except Exception as error:  # model clients expose provider-specific errors
                    self.log_error("explanation failed: %s", type(error).__name__)
                    self._json(
                        HTTPStatus.BAD_GATEWAY,
                        {"error": "explanation_unavailable", "message": "The advisory model is temporarily unavailable."},
                    )
                    return
                self._json(HTTPStatus.OK, explanation)
                return
            similar_match = SIMILAR_PATH.match(path)
            if similar_match:
                self._body()
                gate = self.store.get_gate(unquote(similar_match.group(1)))
                try:
                    result = self.similarity_finder.find(gate, self.store.list_gates())
                except Exception as error:  # search SDK exposes provider-specific errors
                    self.log_error("similarity search failed: %s", type(error).__name__)
                    self._json(
                        HTTPStatus.BAD_GATEWAY,
                        {"error": "similarity_unavailable", "message": "Historical similarity search is temporarily unavailable."},
                    )
                    return
                self._json(HTTPStatus.OK, result)
                return
            match = DECISION_PATH.match(path)
            if not match:
                self._json(HTTPStatus.NOT_FOUND, {"error": "route_not_found"})
                return
            body = self._body()
            result = self.store.decide(
                unquote(match.group(1)),
                action=str(body.get("action", "")),
                actor_id=str(body.get("actor_id", "")),
                reason=str(body.get("reason", "")),
                expected_version=body.get("expected_version"),
                idempotency_key=str(body.get("idempotency_key", "")),
            )
            result["decision_sync"] = {"status": "LOCAL_ONLY"}
            result["workflow_dispatch"] = {"status": "NOT_CONFIGURED"}
            if self.decision_publisher is not None:
                gate = self.store.get_gate(unquote(match.group(1)))
                try:
                    result["decision_sync"] = self.decision_publisher.publish(
                        result["decision"], gate
                    )
                except Exception as error:  # SDK errors are provider-specific
                    self.log_error("decision publish failed: %s", type(error).__name__)
                    result["decision_sync"] = {
                        "status": "PENDING_RETRY",
                        "message": "The review is saved, but CI synchronization is pending.",
                    }
                if (
                    result["decision_sync"].get("status") == "SYNCED"
                    and self.github_dispatcher is not None
                ):
                    try:
                        result["workflow_dispatch"] = self.github_dispatcher.dispatch(
                            result["decision"], gate
                        )
                    except Exception as error:
                        self.log_error("workflow dispatch failed: %s", type(error).__name__)
                        result["workflow_dispatch"] = {
                            "status": "PENDING_RETRY",
                            "message": "The review is governed, but GitHub re-evaluation is pending.",
                        }
            self._json(HTTPStatus.OK, result)
        except GateNotFoundError:
            self._json(HTTPStatus.NOT_FOUND, {"error": "gate_not_found"})
        except ReviewConflictError as error:
            self._json(
                HTTPStatus.CONFLICT,
                {"error": "review_conflict", "current_version": error.current_version},
            )
        except ValidationError as error:
            self._json(HTTPStatus.BAD_REQUEST, {"error": "invalid_request", "message": str(error)})

    def log_message(self, format: str, *args: Any) -> None:
        if os.environ.get("PROOFGATE_QUIET") != "1":
            super().log_message(format, *args)


class ProofGateServer(ThreadingHTTPServer):
    daemon_threads = True

    def __init__(
        self,
        address: tuple[str, int],
        store: GateStore,
        data_mode: str,
        explainer: GateExplainer | None = None,
        similarity_finder: SimilarChangeFinder | None = None,
        synchronizer: WarehouseSynchronizer | None = None,
        decision_publisher: DecisionPublisher | None = None,
        github_dispatcher: GitHubWorkflowDispatcher | None = None,
    ):
        self.store = store
        self.data_mode = data_mode
        self.explainer = explainer or create_explainer()
        self.similarity_finder = similarity_finder or create_similarity_finder()
        self.synchronizer = synchronizer if synchronizer is not None else create_synchronizer()
        self.decision_publisher = (
            decision_publisher
            if decision_publisher is not None
            else create_decision_publisher()
        )
        self.github_dispatcher = (
            github_dispatcher
            if github_dispatcher is not None
            else create_github_dispatcher()
        )
        super().__init__(address, ProofGateHandler)

    def server_close(self) -> None:
        close = getattr(self.store, "close", None)
        if close:
            close()
        super().server_close()


def create_server(
    host: str,
    port: int,
    database_path: str | Path,
    decision_publisher: DecisionPublisher | None = None,
    github_dispatcher: GitHubWorkflowDispatcher | None = None,
) -> ProofGateServer:
    store = SQLiteStore(database_path)
    store.seed_demo()
    return ProofGateServer(
        (host, port),
        store,
        "demo",
        decision_publisher=decision_publisher,
        github_dispatcher=github_dispatcher,
    )


def create_runtime_server(host: str, port: int) -> ProofGateServer:
    mode = os.environ.get("PROOFGATE_STORE", "sqlite").strip().lower()
    if mode == "lakebase":
        from lakebase_store import LakebaseStore

        store: GateStore = LakebaseStore()
        data_mode = "databricks"
        if os.environ.get("PROOFGATE_SEED_DEMO") == "1":
            store.seed_demo()
            data_mode = "demo"
        return ProofGateServer((host, port), store, data_mode)
    if mode != "sqlite":
        raise RuntimeError("PROOFGATE_STORE must be sqlite or lakebase")
    default_database = APP_DIR / ".local" / "proofgate.db"
    database_path = Path(os.environ.get("PROOFGATE_DB_PATH", str(default_database)))
    database_path.parent.mkdir(parents=True, exist_ok=True)
    return create_server(host, port, database_path)


def main() -> None:
    host = os.environ.get("PROOFGATE_HOST", "127.0.0.1")
    port = int(os.environ.get("DATABRICKS_APP_PORT", os.environ.get("PORT", "8000")))
    server = create_runtime_server(host, port)
    print(f"ProofGate Control Room listening on http://{host}:{server.server_port}")
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()


if __name__ == "__main__":
    main()
