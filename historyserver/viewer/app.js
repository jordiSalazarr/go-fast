// The gofast history viewer: read-only pages over the history server's
// JSON API (/v1). The project and token live in sessionStorage, for this
// tab only, and go with every request as a bearer token. Every string from
// the history is set as text; only Markdown the server rendered (raw HTML
// left out) is set as HTML.
"use strict";

const storageKey = "gofast-viewer";
const main = () => document.getElementById("main");

function session() {
  try { return JSON.parse(sessionStorage.getItem(storageKey)) || null; } catch { return null; }
}
function saveSession(s) {
  try { sessionStorage.setItem(storageKey, JSON.stringify(s)); } catch { /* the page still works for this load */ }
}
function clearSession() {
  try { sessionStorage.removeItem(storageKey); } catch { /* nothing kept */ }
}

class Unauthorized extends Error {}

async function api(path, { text = false } = {}) {
  const s = session();
  const resp = await fetch(`/v1/projects/${encodeURIComponent(s.project)}${path}`, {
    headers: { Authorization: `Bearer ${s.token}` },
  });
  if (resp.status === 401) throw new Unauthorized();
  if (!resp.ok) {
    let message = `${resp.status} ${resp.statusText}`;
    try { message = (await resp.json()).message || message; } catch { /* not JSON */ }
    throw new Error(message);
  }
  return text ? resp.text() : resp.json();
}

// el builds an element: attrs are properties or attributes, children are
// nodes or strings (set as text).
function el(tag, attrs = {}, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v === undefined || v === null || v === false) continue;
    if (k === "class") node.className = v;
    else if (k.startsWith("on")) node.addEventListener(k.slice(2), v);
    else node.setAttribute(k, v === true ? "" : v);
  }
  for (const c of children.flat()) {
    if (c === undefined || c === null || c === false) continue;
    node.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return node;
}

const link = (href, ...children) => el("a", { href }, ...children);
const short = (hash) => (hash ? hash.slice(0, 12) : "unversioned");
const when = (t) => (t ? new Date(t).toLocaleString() : "");

function show(...nodes) {
  main().replaceChildren(...nodes);
  window.scrollTo(0, 0);
}

function failed(err) {
  if (err instanceof Unauthorized) {
    clearSession();
    login("The token was refused for this project. Sign in again.");
    return;
  }
  show(el("p", { class: "error" }, `Couldn't load this page: ${err.message}`));
}

// Login.

function login(message) {
  document.getElementById("nav").hidden = true;
  const project = el("input", { name: "project", required: true, autocomplete: "username", placeholder: "wallets" });
  const token = el("input", { name: "token", type: "password", required: true, autocomplete: "current-password" });
  const form = el("form", { class: "login card", onsubmit: (e) => {
    e.preventDefault();
    saveSession({ project: project.value.trim(), token: token.value });
    route();
  } },
  el("h1", {}, "Sign in"),
  el("p", { class: "muted" }, "A read token of the project is enough. It is kept in this tab only."),
  message && el("p", { class: "error" }, message),
  el("label", {}, "Project", project),
  el("label", {}, "Token", token),
  el("button", { type: "submit" }, "Open the history"));
  show(form);
  project.focus();
}

// Routes: #/works[?filters], #/works/<id>, #/instructions,
// #/instructions/<name>, #/diff/<from>/<to>, #/file/<sha256>?path=<path>.

async function route() {
  const s = session();
  if (!s) { login(); return; }
  document.getElementById("nav").hidden = false;
  document.getElementById("project").textContent = `project ${s.project}`;
  const [path, query] = (location.hash.slice(1) || "/works").split("?");
  const params = new URLSearchParams(query || "");
  const parts = path.split("/").filter(Boolean).map(decodeURIComponent);
  try {
    if (parts[0] === "works" && parts[1]) await workPage(parts[1]);
    else if (parts[0] === "instructions" && parts[1]) await instructionPage(parts.slice(1).join("/"));
    else if (parts[0] === "instructions") await instructionsPage();
    else if (parts[0] === "diff" && parts[2]) await diffPage(parts[1], parts[2]);
    else if (parts[0] === "file" && parts[1]) await filePage(parts[1], params.get("path") || parts[1]);
    else await worksPage(params);
  } catch (err) {
    failed(err);
  }
}

// Works.

async function worksPage(params) {
  const field = (name, label, control) => { control.name = name; control.value = params.get(name) || ""; return el("label", {}, label, control); };
  const options = (...values) => values.map((v) => el("option", { value: v }, v || "any"));
  const form = el("form", { class: "filters", onsubmit: (e) => {
    e.preventDefault();
    const q = new URLSearchParams();
    for (const [k, v] of new FormData(form)) if (v) q.set(k, v);
    location.hash = `#/works${q.size ? "?" + q : ""}`;
  } },
  field("q", "Search", el("input", { type: "search", placeholder: "in the description" })),
  field("status", "Status", el("select", {}, options("", "active", "completed", "abandoned"))),
  field("type", "Type", el("select", {}, options("", "fix-bug", "new-feature", "modify-behaviour", "refactor", "improve-performance"))),
  field("branch", "Branch", el("input", {})),
  el("button", { type: "submit" }, "Filter"));
  for (const control of form.querySelectorAll("select")) control.value = params.get(control.name) || "";

  const works = (await api(`/works${params.size ? "?" + params : ""}`)).works;
  const rows = works.slice().reverse().map((w) => el("tr", {},
    el("td", {}, link(`#/works/${encodeURIComponent(w.id)}`, w.description)),
    el("td", {}, w.type), el("td", {}, w.branch),
    el("td", {}, el("span", { class: `status ${w.status}` }, w.status)),
    el("td", { class: "muted" }, when(w.startedAt))));
  show(el("h1", {}, "Works"), form,
    works.length
      ? el("div", { class: "card scroll" }, el("table", {},
        el("thead", {}, el("tr", {}, ["Work", "Type", "Branch", "Status", "Started"].map((h) => el("th", {}, h)))),
        el("tbody", {}, rows)))
      : el("p", { class: "muted" }, "No work matches."));
}

// A work's timeline, by stage visit.

function visitOf(work, e) {
  const p = e.payload || {};
  if (p.assignmentId && p.assignmentId.startsWith(`${work}-`)) {
    const rest = p.assignmentId.slice(work.length + 1);
    const i = rest.lastIndexOf("-");
    return { stage: rest.slice(0, i), visit: Number(rest.slice(i + 1)) };
  }
  switch (e.type) {
    case "FixerEntered": return { stage: "fixer", visit: p.visit };
    case "ReworkStarted": case "WorkReturned": return { stage: p.target, visit: p.visit };
  }
  if (p.stage && p.visit) return { stage: p.stage, visit: p.visit };
  return null;
}

const owner = (o) => (o && o.name ? o.name : "the owner");

// describe says what an event means, and whether it went well ("ok") or
// not ("bad").
function describe(e) {
  const p = e.payload || {};
  switch (e.type) {
    case "WorkStarted": return [`Started as ${p.workType} on branch ${p.branch}: ${p.description}`];
    case "WorkCompleted": return ["Work completed", "ok"];
    case "WorkAbandoned": return [`Abandoned by ${owner(p.owner)}: ${p.reason}`, "bad"];
    case "StageEntered": return [`Entered ${p.stage} (visit ${p.visit}, ${p.gate} gate, ${p.budget} attempts)`];
    case "AssignmentOpened": return [`Opened with ${p.budget} attempts`];
    case "AttemptFailed": return [`Attempt ${p.attempt} failed: ${p.reason}`, "bad"];
    case "SubmittedForApproval": return [`Attempt ${p.attempt} submitted for the owner's approval`];
    case "AssignmentRejected": return [`Rejected by ${owner(p.owner)} (attempt ${p.attempt}): ${p.feedback}`, "bad"];
    case "AssignmentEscalated": return [`Escalated: ${p.attemptsUsed} of ${p.budget} attempts used`, "bad"];
    case "BudgetExtended": return [`${owner(p.owner)} gave ${p.additional} more attempts (${p.newBudget} in all)`];
    case "AssignmentAccepted": {
      const by = e.actor && e.actor.kind === "owner" ? `Approved by ${e.actor.name}` : "Accepted";
      const extra = p.route ? `, routed to ${p.route}: ${p.routeReason}` : p.stress ? `, stress testing ${p.stress}: ${p.stressReason}` : "";
      return [by + extra, "ok"];
    }
    case "AssignmentCancelled": return [`Cancelled: ${p.reason}`];
    case "AssignmentHandedToFixer": return [`Handed to the fixer (attempt ${p.attempt}): ${p.reason}`, "bad"];
    case "FixerEntered": return [`The fixer took over from ${p.from} (visit ${p.fromVisit}), ${p.mode}: ${p.reason}`];
    case "ReworkStarted": return [`The fixer sent the work back to ${p.target}: ${p.reason}`];
    case "ReworkLimitReached": return [`Rework limit reached after ${p.reworks} reworks`, "bad"];
    case "ReworkBudgetExtended": return [`${owner(p.owner)} allowed ${p.additional} more reworks (${p.newBudget} in all)`];
    case "WorkReturned": return [`${owner(p.owner)} sent the work back from ${p.from} to ${p.target}: ${p.reason}`];
    case "StressDecided": return [`Stress testing ${p.decision}${p.owner ? ` by ${owner(p.owner)}` : ""}: ${p.reason}`];
    case "BaselineMeasured": return [`Baseline of ${p.nfr} measured`];
    case "WorkFoundNothingToImprove": return ["Nothing to improve: every target was already met", "ok"];
    case "AgentStarted": return [`Agent ${p.agent} started, version ${short(p.sha256)}`];
    case "SkillLoaded": return [`Skill ${p.skill} ${p.load || "invoked"}, version ${short(p.sha256)}`];
  }
  return [`${e.type} ${JSON.stringify(p)}`];
}

function eventItem(e) {
  const [text, tone] = describe(e);
  const actor = e.actor ? `${e.actor.kind} ${e.actor.name}` : "";
  return el("li", {}, el("span", { class: "muted", title: actor }, when(e.occurredAt)), el("span", { class: `what ${tone || ""}` }, text));
}

function fileLink(f) {
  return link(`#/file/${f.sha256}?path=${encodeURIComponent(f.path)}`, f.path);
}

async function workPage(id) {
  const t = await api(`/works/${encodeURIComponent(id)}`);
  const w = t.work;
  const sections = [];
  const byKey = new Map();
  const general = [];
  for (const e of t.events) {
    const v = visitOf(w.id, e);
    if (!v) { general.push(e); continue; }
    const key = `${v.stage} ${v.visit}`;
    if (!byKey.has(key)) { byKey.set(key, { ...v, events: [] }); sections.push(byKey.get(key)); }
    byKey.get(key).events.push(e);
  }
  const instructions = new Map(t.visits.map((v) => [`${v.stage} ${v.visit}`, v.instructions]));
  const artifacts = (s) => t.files.filter((f) => f.path.split("/").pop().startsWith(`${s.stage}-v${s.visit}.`) || f.path.split("/").pop().startsWith(`${s.stage}-v${s.visit}-`));

  show(
    el("p", {}, link("#/works", "← Works")),
    el("h1", {}, w.description),
    el("p", { class: "muted" }, `${w.type} · branch ${w.branch} · started ${when(w.startedAt)} · `, el("span", { class: `status ${w.status}` }, w.status), ` · ${w.id}`),
    general.length && el("div", { class: "card" }, el("h2", {}, "The work"), el("ol", { class: "events" }, general.map(eventItem))),
    sections.map((s) => el("div", { class: "card" },
      el("h2", {}, `${s.stage} · visit ${s.visit}`),
      (instructions.get(`${s.stage} ${s.visit}`) || []).length > 0 && el("div", { class: "chips" },
        instructions.get(`${s.stage} ${s.visit}`).map((i) => el("span", { class: "chip" },
          `${i.kind} `, link(`#/instructions/${encodeURIComponent(i.name)}`, i.name),
          i.load ? ` (${i.load})` : "", " ", el("span", { class: "hash" }, short(i.version))))),
      artifacts(s).length > 0 && el("p", {}, "Artifacts: ", artifacts(s).map((f, i) => [i ? ", " : "", fileLink(f)])),
      el("ol", { class: "events" }, s.events.map(eventItem)))),
    el("div", { class: "card" }, el("h2", {}, "Files"),
      t.files.length ? el("ul", {}, t.files.map((f) => el("li", {}, fileLink(f), " ", el("span", { class: "hash muted" }, short(f.sha256)))))
        : el("p", { class: "muted" }, "None synced.")));
}

// Skills and agents.

async function instructionsPage() {
  const names = (await api("/instructions")).names;
  show(el("h1", {}, "Skills and agents"),
    el("p", { class: "muted" }, "Every skill and agent the history names, and how many versions of each instructed a stage."),
    el("div", { class: "card scroll" }, el("table", {},
      el("thead", {}, el("tr", {}, ["Name", "Kind", "Versions", "Last used"].map((h) => el("th", {}, h)))),
      el("tbody", {}, names.map((n) => el("tr", {},
        el("td", {}, link(`#/instructions/${encodeURIComponent(n.name)}`, n.name)),
        el("td", {}, n.kind), el("td", {}, String(n.versions)), el("td", { class: "muted" }, when(n.lastSeen))))))));
}

async function instructionPage(name) {
  const it = await api(`/instructions?name=${encodeURIComponent(name)}`);
  const versioned = it.versions.filter((v) => v.version);
  show(el("p", {}, link("#/instructions", "← Skills and agents")),
    el("h1", {}, `${it.kind} ${it.name}`),
    it.versions.map((v) => {
      const i = versioned.indexOf(v);
      const previous = i > 0 ? versioned[i - 1] : null;
      return el("div", { class: "card" },
        el("h2", {}, el("span", { class: "hash" }, short(v.version)), el("span", { class: "muted" }, ` · first used ${when(v.firstSeen)}`)),
        previous && el("p", {}, link(`#/diff/${previous.version}/${v.version}`, `What changed since ${short(previous.version)}`)),
        v.files.length > 0 && el("p", {}, "Files: ", v.files.map((f, j) => [j ? ", " : "", fileLink(f)])),
        !v.version && el("p", { class: "muted" }, "Loaded without a version: another plugin's skill, or gf didn't know where the plugin was."),
        el("ol", { class: "events" }, v.uses.map((u) => el("li", {},
          el("span", { class: "muted" }, when(u.at)),
          el("span", {}, link(`#/works/${encodeURIComponent(u.work)}`, u.work), ` · ${u.stage} visit ${u.visit}${u.load ? ` · ${u.load}` : ""}`)))));
    }));
}

async function diffPage(from, to) {
  const d = await api(`/instructions/${from}/diff/${to}`);
  show(el("p", {}, el("a", { href: "#", onclick: (e) => { e.preventDefault(); history.back(); } }, "← Back")),
    el("h1", {}, "What changed: ", el("span", { class: "hash" }, short(from)), " → ", el("span", { class: "hash" }, short(to))),
    d.files.map((f) => el("div", { class: "card" },
      el("h2", {}, f.path, el("span", { class: "muted" }, ` · ${f.status}`)),
      f.unified && el("pre", { class: "diff" }, f.unified.split("\n").filter((l, i, all) => i < all.length - 1 || l).map((l) =>
        el("span", { class: l.startsWith("@@") ? "hunk" : l.startsWith("+") && !l.startsWith("+++") ? "add" : l.startsWith("-") && !l.startsWith("---") ? "del" : "" }, l))))));
}

// A file: Markdown rendered by the server, anything else as text.

async function filePage(sha, path) {
  const header = [el("p", {}, el("a", { href: "#", onclick: (e) => { e.preventDefault(); history.back(); } }, "← Back")),
    el("h1", {}, path), el("p", { class: "muted hash" }, sha)];
  if (path.endsWith(".md")) {
    const rendered = await api(`/blobs/${sha}/html`);
    const body = el("div", { class: "card rendered" });
    body.innerHTML = rendered.html; // goldmark's output, raw HTML left out
    show(...header, body);
    return;
  }
  show(...header, el("pre", {}, await api(`/blobs/${sha}`, { text: true })));
}

window.addEventListener("hashchange", route);
window.addEventListener("DOMContentLoaded", () => {
  document.getElementById("logout").addEventListener("click", () => { clearSession(); location.hash = ""; login(); });
  route();
});
