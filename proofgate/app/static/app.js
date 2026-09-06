"use strict";

const state = {
  gates: [],
  selectedId: null,
  selectedGate: null,
  filter: "ALL",
  pendingAction: null,
};

const $ = (id) => document.getElementById(id);
const gateList = $("gate-list");
const evidencePanel = $("evidence-panel");
const dialog = $("decision-dialog");
const form = $("decision-form");

function element(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

async function api(path, options = {}) {
  const response = await fetch(path, {
    headers: { "Content-Type": "application/json", ...(options.headers || {}) },
    ...options,
  });
  const payload = await response.json().catch(() => ({ error: "invalid_response" }));
  if (!response.ok) {
    const error = new Error(payload.message || payload.error || `Request failed: ${response.status}`);
    error.status = response.status;
    error.payload = payload;
    throw error;
  }
  return payload;
}

function badge(status) {
  return element("span", `badge badge-${status.toLowerCase()}`, status.replaceAll("_", " "));
}

function riskClass(score) {
  if (score >= 70) return "risk-high";
  if (score >= 30) return "risk-medium";
  return "risk-low";
}

function formatTime(value) {
  return new Intl.DateTimeFormat(undefined, {
    month: "short", day: "numeric", hour: "2-digit", minute: "2-digit",
  }).format(new Date(value));
}

function visibleGates() {
  if (state.filter === "PENDING") return state.gates.filter((gate) => gate.review_status === "PENDING");
  if (state.filter === "RESOLVED") return state.gates.filter((gate) => gate.review_status !== "PENDING");
  return state.gates;
}

function renderGates() {
  gateList.replaceChildren();
  const gates = visibleGates();
  if (!gates.length) {
    gateList.append(element("div", "no-gates", "No gates match this filter."));
    return;
  }
  for (const gate of gates) {
    const button = element("button", `gate-card${gate.event_id === state.selectedId ? " is-selected" : ""}`);
    button.type = "button";
    button.addEventListener("click", () => selectGate(gate.event_id));

    const copy = element("div");
    const title = element("div", "gate-title-row");
    title.append(element("span", "gate-repo", gate.repo_id), element("span", "gate-sha", gate.commit_sha));
    copy.append(title, element("p", "gate-summary", gate.summary));
    const meta = element("div", "gate-meta");
    meta.append(badge(gate.review_status), element("span", "", formatTime(gate.occurred_at)));
    copy.append(meta);

    const risk = element("div", `risk ${riskClass(gate.risk_score)}`, String(gate.risk_score));
    risk.append(element("small", "", "RISK"));
    button.append(copy, risk);
    gateList.append(button);
  }
}

function evidenceCell(label, value) {
  const cell = element("div", "evidence-cell");
  cell.append(element("span", "", label), element("strong", "", value));
  return cell;
}

function renderEvidence() {
  evidencePanel.replaceChildren();
  const gate = state.selectedGate;
  if (!gate) {
    const empty = element("div", "empty-state");
    empty.append(element("span", "", "⌁"), element("h2", "", "Select a policy gate"), element("p", "", "Inspect its Entire provenance, test evidence, policy reasons, and review history."));
    evidencePanel.append(empty);
    return;
  }

  const heading = element("div", "evidence-heading");
  const headingCopy = element("div");
  headingCopy.append(element("p", "eyebrow", "Change passport"), element("h2", "", `${gate.repo_id} · ${gate.commit_sha}`), element("span", "checkpoint", gate.checkpoint_id));
  const orb = element("div", `risk-orb ${riskClass(gate.risk_score)}`);
  orb.append(element("strong", "", String(gate.risk_score)), element("small", "", "RISK"));
  heading.append(headingCopy, orb);
  evidencePanel.append(heading, element("div", "summary-box", gate.summary));

  const grid = element("div", "evidence-grid");
  grid.append(
    evidenceCell("Automated verdict", gate.automated_verdict.replaceAll("_", " ")),
    evidenceCell("Review state", gate.review_status.replaceAll("_", " ")),
    evidenceCell("Test evidence", `${gate.tests_total - gate.tests_failed}/${gate.tests_total} passed`),
    evidenceCell("Entire provenance", gate.provenance_complete ? "Complete" : "Incomplete"),
    evidenceCell("Impact analysis", `${gate.impact_analysis_source} · ${gate.impact_analysis_complete ? "complete" : "partial"}`),
    evidenceCell("Maximum dependents", String(gate.max_dependent_count)),
    evidenceCell("Changed files", String(gate.changed_file_count)),
    evidenceCell("Review version", `v${gate.version}`),
  );
  evidencePanel.append(grid, element("p", "section-label", "Policy reasons"));

  const reasons = element("div", "reason-list");
  if (!gate.reason_codes.length) reasons.append(element("div", "clean-reasons", "✓ No policy exceptions detected"));
  for (const reason of gate.reason_codes) {
    const hard = gate.hard_stop_codes.includes(reason);
    reasons.append(element("div", `reason${hard ? "" : " is-soft"}`, reason));
  }
  evidencePanel.append(reasons);

  if (gate.review_status === "PENDING") {
    const actions = element("div", "review-actions");
    const reject = element("button", "button button-danger", "Reject change");
    reject.type = "button";
    reject.addEventListener("click", () => openDecision("REJECT"));
    const approve = element("button", "button button-primary", "Approve with reason");
    approve.type = "button";
    approve.addEventListener("click", () => openDecision("APPROVE"));
    actions.append(reject, approve);
    evidencePanel.append(actions);
  } else {
    const banner = element("div", "review-banner");
    banner.append(element("span", "", "Current review decision"), element("strong", "", gate.review_status.replaceAll("_", " ")));
    evidencePanel.append(banner);
  }

  if (gate.decisions.length) {
    evidencePanel.append(element("p", "section-label", "Immutable review history"));
    const audit = element("div", "audit-list");
    for (const decision of gate.decisions) {
      const item = element("div", "audit-item");
      const firstLine = element("div");
      firstLine.append(element("strong", "", decision.action), document.createTextNode(` by ${decision.actor_id} · ${formatTime(decision.occurred_at)}`));
      item.append(firstLine, element("div", "", decision.reason));
      audit.append(item);
    }
    evidencePanel.append(audit);
  }

  evidencePanel.append(element("p", "section-label", "AI evidence brief"));
  const aiBox = element("div", "ai-box");
  const aiCopy = element("p", "", "Generate a short advisory explanation from allowlisted evidence only. It cannot change this verdict.");
  const explainButton = element("button", "button button-subtle", "Explain evidence");
  explainButton.type = "button";
  explainButton.addEventListener("click", () => explainGate(explainButton, aiCopy));
  aiBox.append(aiCopy, explainButton);
  evidencePanel.append(aiBox);

  evidencePanel.append(element("p", "section-label", "Similar historical evidence"));
  const similarBox = element("div", "similar-box");
  const similarIntro = element("p", "", "Compare this gate with prior categorical risk patterns—without sending source code or raw prompts.");
  const similarButton = element("button", "button button-subtle", "Find similar changes");
  similarButton.type = "button";
  similarButton.addEventListener("click", () => findSimilar(similarButton, similarBox, similarIntro));
  similarBox.append(similarIntro, similarButton);
  evidencePanel.append(similarBox);
}

async function explainGate(button, output) {
  button.disabled = true;
  button.textContent = "Explaining…";
  try {
    const result = await api(`/api/gates/${encodeURIComponent(state.selectedGate.event_id)}/explain`, {
      method: "POST", body: "{}",
    });
    output.textContent = result.explanation;
    if (String(result.generated_by).startsWith("databricks-")) {
      button.textContent = "Databricks AI";
    } else if (result.generated_by === "deterministic-fallback") {
      button.textContent = "Safe fallback";
    } else {
      button.textContent = "Local preview";
    }
  } catch (error) {
    output.textContent = `Explanation unavailable: ${error.message}`;
    button.textContent = "Try again";
    button.disabled = false;
  }
}

async function findSimilar(button, box, intro) {
  button.disabled = true;
  button.textContent = "Searching…";
  try {
    const result = await api(`/api/gates/${encodeURIComponent(state.selectedGate.event_id)}/similar`, {
      method: "POST", body: "{}",
    });
    box.replaceChildren();
    if (!result.matches.length) {
      box.append(element("p", "", "No comparable historical evidence found."));
      return;
    }
    for (const match of result.matches) {
      const row = element("div", "similar-row");
      const copy = element("div");
      copy.append(element("strong", "", `${match.repo_id} · ${match.decision.replaceAll("_", " ")}`), element("small", "", match.checkpoint_id));
      const score = match.similarity_score == null ? "—" : `${Math.round(Number(match.similarity_score) * 100)}%`;
      row.append(copy, element("span", riskClass(match.risk_score), `${score} similar`));
      box.append(row);
    }
    box.append(element("small", "similar-source", result.generated_by === "databricks-ai-search" ? "Retrieved by Databricks AI Search" : "Local deterministic preview"));
  } catch (error) {
    intro.textContent = `Similarity unavailable: ${error.message}`;
    button.textContent = "Try again";
    button.disabled = false;
  }
}

async function selectGate(eventId) {
  state.selectedId = eventId;
  renderGates();
  evidencePanel.replaceChildren(element("div", "loading-card", "Loading passport…"));
  try {
    const payload = await api(`/api/gates/${encodeURIComponent(eventId)}`);
    state.selectedGate = payload.gate;
    renderEvidence();
  } catch (error) {
    state.selectedGate = null;
    evidencePanel.replaceChildren(element("div", "no-gates", error.message));
  }
}

function updateMetrics(metrics) {
  $("metric-pending").textContent = metrics.pending;
  $("metric-risk").textContent = metrics.average_risk_score;
  $("metric-provenance").textContent = metrics.provenance_rate;
  $("metric-resolved").textContent = metrics.approved + metrics.rejected + metrics.auto_pass;
  $("queue-count").textContent = metrics.pending;
}

async function refresh({ keepSelection = true } = {}) {
  const [gatePayload, metricPayload] = await Promise.all([api("/api/gates"), api("/api/metrics")]);
  state.gates = gatePayload.gates;
  const isDemo = gatePayload.data_mode === "demo";
  $("data-mode-title").textContent = isDemo ? "Local demo" : "Databricks live";
  $("data-mode-copy").textContent = isDemo ? "Seeded, synthetic evidence" : "Lakebase review state";
  $("data-mode-chip").lastChild.textContent = isDemo ? " DEMO DATA" : " LIVE DATA";
  $("reset-button").hidden = !isDemo;
  $("sync-button").hidden = isDemo;
  updateMetrics(metricPayload.metrics);
  if (!keepSelection || !state.gates.some((gate) => gate.event_id === state.selectedId)) {
    state.selectedId = state.gates[0]?.event_id || null;
  }
  renderGates();
  if (state.selectedId) await selectGate(state.selectedId);
}

function openDecision(action) {
  state.pendingAction = action;
  const approve = action === "APPROVE";
  $("dialog-symbol").textContent = approve ? "✓" : "×";
  $("dialog-symbol").style.background = approve ? "var(--lime)" : "var(--red)";
  $("dialog-title").textContent = approve ? "Approve change" : "Reject change";
  $("dialog-copy").textContent = `${approve ? "Approve" : "Reject"} ${state.selectedGate.repo_id} at ${state.selectedGate.commit_sha}. The automated ${state.selectedGate.automated_verdict.replaceAll("_", " ")} verdict remains immutable.`;
  $("confirm-decision").textContent = approve ? "Confirm approval" : "Confirm rejection";
  $("confirm-decision").className = approve ? "button button-primary" : "button button-danger";
  $("reason-input").value = "";
  $("dialog-error").textContent = "";
  dialog.showModal();
  $("reason-input").focus();
}

function showToast(message) {
  const toast = $("toast");
  toast.textContent = message;
  toast.classList.add("is-visible");
  window.setTimeout(() => toast.classList.remove("is-visible"), 2600);
}

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  if (!state.selectedGate || !state.pendingAction) return;
  const confirm = $("confirm-decision");
  confirm.disabled = true;
  $("dialog-error").textContent = "";
  try {
    const result = await api(`/api/gates/${encodeURIComponent(state.selectedGate.event_id)}/decision`, {
      method: "POST",
      body: JSON.stringify({
        action: state.pendingAction,
        actor_id: $("actor-input").value,
        reason: $("reason-input").value,
        expected_version: state.selectedGate.version,
        idempotency_key: crypto.randomUUID(),
      }),
    });
    dialog.close();
    const synchronized = result.decision_sync?.status === "SYNCED";
    const dispatched = result.workflow_dispatch?.status === "DISPATCHED";
    if (state.pendingAction === "APPROVE" && synchronized && dispatched) {
      showToast("Approval governed; GitHub re-evaluation dispatched.");
    } else if (synchronized) {
      showToast(`${state.pendingAction === "APPROVE" ? "Approval" : "Rejection"} synced to the CI gate.`);
    } else {
      showToast(`${state.pendingAction === "APPROVE" ? "Approval" : "Rejection"} recorded; CI sync is ${result.decision_sync?.status || "local"}.`);
    }
    await refresh();
  } catch (error) {
    $("dialog-error").textContent = error.status === 409
      ? "Another reviewer updated this gate. Close and reopen it to use the latest version."
      : error.message;
  } finally {
    confirm.disabled = false;
  }
});

$("cancel-decision").addEventListener("click", () => dialog.close());

$("reset-button").addEventListener("click", async () => {
  const button = $("reset-button");
  button.disabled = true;
  try {
    await api("/api/demo/reset", { method: "POST", body: "{}" });
    state.selectedId = null;
    await refresh({ keepSelection: false });
    showToast("Synthetic demo evidence reset.");
  } catch (error) {
    showToast(error.message);
  } finally {
    button.disabled = false;
  }
});

$("sync-button").addEventListener("click", async () => {
  const button = $("sync-button");
  button.disabled = true;
  button.textContent = "Syncing…";
  try {
    const result = await api("/api/admin/sync", { method: "POST", body: "{}" });
    await refresh({ keepSelection: true });
    showToast(`${result.upserted} governed changes synchronized.`);
  } catch (error) {
    showToast(error.message);
  } finally {
    button.textContent = "Sync evidence";
    button.disabled = false;
  }
});

for (const button of document.querySelectorAll(".filter-button")) {
  button.addEventListener("click", () => {
    state.filter = button.dataset.filter;
    document.querySelectorAll(".filter-button").forEach((candidate) => candidate.classList.toggle("is-active", candidate === button));
    renderGates();
  });
}

refresh().catch((error) => {
  gateList.replaceChildren(element("div", "no-gates", `Could not load ProofGate: ${error.message}`));
});
