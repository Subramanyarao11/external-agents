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
from typing import Any
from urllib.parse import unquote, urlparse

from store import GateNotFoundError, ReviewConflictError, SQLiteStore, ValidationError


APP_DIR = Path(__file__).resolve().parent
STATIC_DIR = APP_DIR / "static"
GATE_PATH = re.compile(r"^/api/gates/([^/]+)$")
DECISION_PATH = re.compile(r"^/api/gates/([^/]+)/decision$")


class ProofGateHandler(SimpleHTTPRequestHandler):
    server_version = "ProofGate/0.1"

    def __init__(self, *args: Any, **kwargs: Any):
        super().__init__(*args, directory=str(STATIC_DIR), **kwargs)

    @property
    def store(self) -> SQLiteStore:
        return self.server.store  # type: ignore[attr-defined]

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
            self._json(HTTPStatus.OK, {"status": "ok", "mode": "local-demo"})
            return
        if path == "/api/gates":
            self._json(HTTPStatus.OK, {"gates": self.store.list_gates(), "data_mode": "demo"})
            return
        if path == "/api/metrics":
            self._json(HTTPStatus.OK, {"metrics": self.store.metrics(), "data_mode": "demo"})
            return
        match = GATE_PATH.match(path)
        if match:
            try:
                gate = self.store.get_gate(unquote(match.group(1)))
            except GateNotFoundError:
                self._json(HTTPStatus.NOT_FOUND, {"error": "gate_not_found"})
                return
            self._json(HTTPStatus.OK, {"gate": gate, "data_mode": "demo"})
            return
        if path == "/":
            self.path = "/index.html"
        super().do_GET()

    def do_POST(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler API
        path = urlparse(self.path).path
        try:
            if path == "/api/demo/reset":
                self._body()
                result = self.store.seed_demo(reset=True)
                self._json(HTTPStatus.OK, {"reset": True, **result})
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

    def __init__(self, address: tuple[str, int], store: SQLiteStore):
        self.store = store
        super().__init__(address, ProofGateHandler)


def create_server(host: str, port: int, database_path: str | Path) -> ProofGateServer:
    store = SQLiteStore(database_path)
    store.seed_demo()
    return ProofGateServer((host, port), store)


def main() -> None:
    host = os.environ.get("PROOFGATE_HOST", "127.0.0.1")
    port = int(os.environ.get("DATABRICKS_APP_PORT", os.environ.get("PORT", "8000")))
    default_database = APP_DIR / ".local" / "proofgate.db"
    database_path = Path(os.environ.get("PROOFGATE_DB_PATH", str(default_database)))
    database_path.parent.mkdir(parents=True, exist_ok=True)
    server = create_server(host, port, database_path)
    print(f"ProofGate Control Room listening on http://{host}:{server.server_port}")
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()


if __name__ == "__main__":
    main()

