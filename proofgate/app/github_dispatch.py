"""Dispatch a ProofGate re-evaluation after a governed human approval."""

from __future__ import annotations

import json
import os
import re
import urllib.error
import urllib.parse
import urllib.request
from typing import Any, Callable


REPOSITORY = re.compile(r"^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$")
WORKFLOW = re.compile(r"^(?:[A-Za-z0-9_.-]+\.ya?ml|[1-9][0-9]*)$")
COMMIT_SHA = re.compile(r"^[0-9a-fA-F]{7,40}$")


class GitHubWorkflowDispatcher:
    """Call GitHub's workflow_dispatch endpoint without exposing credentials."""

    def __init__(
        self,
        repository: str,
        workflow: str,
        workflow_ref: str,
        token: str,
        *,
        opener: Callable[[urllib.request.Request, float], Any] | None = None,
    ) -> None:
        if not REPOSITORY.fullmatch(repository):
            raise ValueError("repository must be in owner/name form")
        if not WORKFLOW.fullmatch(workflow):
            raise ValueError("workflow must be a .yml/.yaml filename or numeric workflow ID")
        if not workflow_ref.strip() or len(workflow_ref) > 255:
            raise ValueError("workflow_ref is required and must be at most 255 characters")
        if not token.strip():
            raise ValueError("token is required")
        self.repository = repository
        self.workflow = workflow
        self.workflow_ref = workflow_ref
        self._token = token
        self._opener = opener or self._open

    @staticmethod
    def _open(request: urllib.request.Request, timeout: float) -> Any:
        return urllib.request.urlopen(request, timeout=timeout)

    def dispatch(self, decision: dict[str, Any], gate: dict[str, Any]) -> dict[str, str]:
        if str(decision.get("action", "")).upper() != "APPROVE":
            return {"status": "SKIPPED_NOT_APPROVED"}
        commit_sha = str(gate.get("commit_sha", "")).strip()
        if not COMMIT_SHA.fullmatch(commit_sha):
            raise ValueError("gate commit_sha must be a 7-40 character hexadecimal SHA")

        encoded_workflow = urllib.parse.quote(self.workflow, safe="")
        url = (
            f"https://api.github.com/repos/{self.repository}/actions/workflows/"
            f"{encoded_workflow}/dispatches"
        )
        payload = json.dumps(
            {"ref": self.workflow_ref, "inputs": {"candidate_ref": commit_sha}},
            separators=(",", ":"),
        ).encode("utf-8")
        request = urllib.request.Request(
            url,
            data=payload,
            method="POST",
            headers={
                "Accept": "application/vnd.github+json",
                "Authorization": f"Bearer {self._token}",
                "Content-Type": "application/json",
                "User-Agent": "proofgate-control-room",
                "X-GitHub-Api-Version": "2022-11-28",
            },
        )
        try:
            response = self._opener(request, 15.0)
            with response:
                status = int(response.status)
                request_id = str(response.headers.get("X-GitHub-Request-Id", ""))
        except urllib.error.HTTPError as error:
            try:
                request_id = str(error.headers.get("X-GitHub-Request-Id", ""))
            finally:
                error.close()
            raise RuntimeError(
                f"GitHub workflow dispatch failed with HTTP {error.code}"
                + (f" (request {request_id})" if request_id else "")
            ) from error
        if status != 204:
            raise RuntimeError(f"GitHub workflow dispatch returned HTTP {status}")
        result = {"status": "DISPATCHED", "candidate_ref": commit_sha}
        if request_id:
            result["request_id"] = request_id
        return result


def create_github_dispatcher() -> GitHubWorkflowDispatcher | None:
    repository = os.environ.get("PROOFGATE_GITHUB_REPOSITORY", "").strip()
    token = os.environ.get("PROOFGATE_GITHUB_TOKEN", "").strip()
    if not repository and not token:
        return None
    if not repository or not token:
        raise RuntimeError(
            "PROOFGATE_GITHUB_REPOSITORY and PROOFGATE_GITHUB_TOKEN must be set together"
        )
    return GitHubWorkflowDispatcher(
        repository=repository,
        workflow=os.environ.get("PROOFGATE_GITHUB_WORKFLOW", "proofgate.yml").strip(),
        workflow_ref=os.environ.get("PROOFGATE_GITHUB_WORKFLOW_REF", "main").strip(),
        token=token,
    )
