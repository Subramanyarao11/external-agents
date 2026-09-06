#!/usr/bin/env python3
"""Publish one continuously updated, allowlisted ProofGate PR report."""

from __future__ import annotations

import argparse
import json
import os
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any


MARKER = "<!-- proofgate-report -->"


def _text(value: Any, fallback: str = "Not reported") -> str:
    rendered = str(value).strip() if value is not None else ""
    if not rendered:
        rendered = fallback
    return rendered.replace("|", "\\|").replace("\r", " ").replace("\n", " ")


def _list(values: Any, fallback: str = "None") -> str:
    if not isinstance(values, list) or not values:
        return fallback
    return ", ".join(f"`{_text(value)}`" for value in values)


def _percent(value: Any) -> str:
    try:
        return f"{float(value) * 100:.0f}%"
    except (TypeError, ValueError):
        return "Not reported"


def render_report(payload: dict[str, Any], run_url: str) -> str:
    passport = payload.get("passport") or {}
    change = passport.get("change") or {}
    authoring = passport.get("authoring") or {}
    impact = passport.get("impact") or {}
    tests = passport.get("tests") or {}
    history = passport.get("history") or {}
    safety = passport.get("safety") or {}
    result = payload.get("result") or {}
    databricks = payload.get("databricks") or {}

    enforcement = _text(payload.get("enforcement_status"), "UNKNOWN")
    checkpoint = _text(change.get("checkpoint_id"), "Missing")
    tool_categories = _list(authoring.get("tool_categories"))
    changed_files = impact.get("changed_files") or []
    impacted_entities = impact.get("impacted_entities") or []
    passed = max(int(tests.get("total") or 0) - int(tests.get("failed") or 0), 0)

    lines = [
        MARKER,
        f"## 🛡️ ProofGate: {enforcement.replace('_', ' ')}",
        "",
        "| Evidence | Result |",
        "| --- | --- |",
        f"| Evaluated commit | `{_text(change.get('commit_sha'))}` |",
        f"| Automated verdict | **{_text(result.get('decision'), 'UNKNOWN')}** |",
        f"| Risk | **{int(result.get('score') or 0)} / 100** · {len(result.get('hard_stops') or [])} hard stop(s) |",
        f"| Entire checkpoint | `{checkpoint}` · provenance {'complete' if authoring.get('provenance_complete') else 'incomplete'} |",
        f"| Authoring | {_text(authoring.get('agent_family'))} / {_text(authoring.get('model_family'))} · {int(authoring.get('session_count') or 0)} session(s) · {int(authoring.get('handoff_count') or 0)} handoff(s) |",
        f"| Tool categories | {tool_categories} |",
        f"| Entire Graph | {_text(impact.get('analysis_source'))} · {'complete' if impact.get('analysis_complete') else 'partial'} · depth {int(impact.get('dependency_depth') or 0)} · max {int(impact.get('max_dependent_count') or 0)} dependents |",
        f"| Change surface | {len(changed_files)} files · {int(impact.get('changed_line_count') or 0)} lines · {len(impacted_entities)} impacted entities |",
        f"| Tests | {passed}/{int(tests.get('total') or 0)} passed · required suite {'missing' if tests.get('required_test_missing') else 'present'} |",
        f"| Databricks | {_text(databricks.get('status'), 'OFF')} · history {'loaded' if history.get('available') else 'unavailable'} |",
        f"| Historical context | {int(history.get('baseline_change_count') or 0)} changes / {int(history.get('baseline_window_days') or 0)} days · similar failure {_percent(history.get('similar_failure_rate'))} |",
        f"| Policy integrity | `{_text(result.get('policy_version'))}` · `{_text(result.get('passport_fingerprint'))}` |",
        "",
    ]

    hard_stops = result.get("hard_stops") or []
    reasons = result.get("risk_reasons") or []
    if hard_stops:
        lines.extend(["### Required action", ""])
        for stop in hard_stops:
            lines.append(f"- **{_text(stop.get('code'))}:** {_text(stop.get('message'))}")
        lines.append("")

    if reasons:
        lines.extend(["<details><summary>Risk evidence</summary>", ""])
        for reason in reasons:
            lines.append(
                f"- **{_text(reason.get('code'))}** (+{int(reason.get('points') or 0)}): "
                f"{_text(reason.get('message'))}"
            )
        lines.extend(["", "</details>", ""])

    focus = result.get("recommended_review_focus") or []
    uncertainties = result.get("uncertainties") or []
    if focus or uncertainties:
        lines.extend(["<details><summary>Review focus and uncertainty</summary>", ""])
        for item in focus:
            lines.append(f"- Review: {_text(item)}")
        for item in uncertainties:
            lines.append(f"- Uncertainty: {_text(item)}")
        lines.extend(["", "</details>", ""])

    lines.extend(
        [
            f"[Open the full job summary and Change Passport artifact]({_text(run_url)})",
            "",
            f"> Allowlisted metadata only. Redaction `{_text(safety.get('redaction_version'))}`; "
            f"{int(safety.get('dropped_field_count') or 0)} field(s) dropped. "
            "No raw prompts, chain-of-thought, secrets, or source code are included.",
        ]
    )
    return "\n".join(lines) + "\n"


class GitHubComments:
    def __init__(self, repository: str, token: str, api_url: str) -> None:
        if repository.count("/") != 1:
            raise ValueError("repository must be OWNER/REPOSITORY")
        if not token:
            raise ValueError("GITHUB_TOKEN is required")
        self.repository = repository
        self.token = token
        self.api_url = api_url.rstrip("/")

    def _request(self, path: str, *, method: str = "GET", body: dict[str, Any] | None = None) -> Any:
        data = None if body is None else json.dumps(body, separators=(",", ":")).encode("utf-8")
        request = urllib.request.Request(
            f"{self.api_url}{path}",
            data=data,
            method=method,
            headers={
                "Accept": "application/vnd.github+json",
                "Authorization": f"Bearer {self.token}",
                "Content-Type": "application/json",
                "User-Agent": "proofgate-pr-reporter",
                "X-GitHub-Api-Version": "2022-11-28",
            },
        )
        try:
            response = urllib.request.urlopen(request, timeout=20)
        except urllib.error.HTTPError as error:
            try:
                detail = error.read(1024).decode("utf-8", errors="replace")
            finally:
                error.close()
            raise RuntimeError(f"GitHub comment request failed with HTTP {error.code}: {detail}") from error
        with response:
            return json.load(response) if response.length != 0 else None

    def upsert(self, pull_request: int, body: str) -> str:
        comments = self._request(
            f"/repos/{self.repository}/issues/{pull_request}/comments?per_page=100"
        )
        existing = next(
            (
                comment
                for comment in comments
                if MARKER in str(comment.get("body", ""))
                and str((comment.get("user") or {}).get("type", "")) == "Bot"
            ),
            None,
        )
        if existing:
            result = self._request(
                f"/repos/{self.repository}/issues/comments/{int(existing['id'])}",
                method="PATCH",
                body={"body": body},
            )
        else:
            result = self._request(
                f"/repos/{self.repository}/issues/{pull_request}/comments",
                method="POST",
                body={"body": body},
            )
        return str(result.get("html_url", ""))


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--result", required=True)
    parser.add_argument("--repository", required=True)
    parser.add_argument("--pull-request", type=int, required=True)
    parser.add_argument("--run-url", required=True)
    arguments = parser.parse_args()

    payload = json.loads(Path(arguments.result).read_text(encoding="utf-8"))
    report = render_report(payload, arguments.run_url)
    comments = GitHubComments(
        arguments.repository,
        os.environ.get("GITHUB_TOKEN", ""),
        os.environ.get("GITHUB_API_URL", "https://api.github.com"),
    )
    print(comments.upsert(arguments.pull_request, report))


if __name__ == "__main__":
    main()
