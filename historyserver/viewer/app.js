// The gofast history viewer: pages over the history server's JSON API
// (/v1), where a member inspects the projects of their organisation and
// assesses their works and stage visits. The member's token and the
// project they're on live in sessionStorage, for this tab only, and the
// token goes with every request as a bearer token. Every string from the
// history is set as text; only Markdown the server rendered (raw HTML left
// out) is set as HTML.
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

// api calls the project's path; a path from the root (/v1/...) is called
// as is. A body is sent as JSON, with PUT unless method says otherwise.
async function api(path, { text = false, body, method } = {}) {
  const s = session();
  const url = path.startsWith("/v1/") ? path : `/v1/projects/${encodeURIComponent(s.project)}${path}`;
  const headers = { Authorization: `Bearer ${s.token}` };
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const resp = await fetch(url, { method: method || (body === undefined ? "GET" : "PUT"), headers, body: body === undefined ? undefined : JSON.stringify(body) });
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

// show puts nodes on the page: arrays of them flattened, and false, null,
// undefined and 0 (a skipped "list.length && …") left out.
function show(...nodes) {
  main().replaceChildren(...nodes.flat(Infinity).filter((n) => n !== false && n !== null && n !== undefined && n !== 0));
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
  const token = el("input", { name: "token", type: "password", required: true, autocomplete: "current-password" });
  const form = el("form", { class: "login card", onsubmit: async (e) => {
    e.preventDefault();
    saveSession({ token: token.value });
    try {
      await refreshMe(token.value);
      if (!session().project) location.hash = "#/projects";
      route();
    } catch (err) {
      clearSession();
      login(err instanceof Unauthorized ? "The server doesn't know this token." : `Couldn't sign in: ${err.message}`);
    }
  } },
  el("h1", {}, "Sign in"),
  el("p", { class: "muted" }, "With your member token: you see every project of your organisation. It is kept in this tab only."),
  message && el("p", { class: "error" }, message),
  el("label", {}, "Member token", token),
  el("button", { type: "submit" }, "Open the history"));
  show(form);
  token.focus();
}

// refreshMe keeps who the member is, and their organisation's projects, in
// the session; it stays on the project it was on while that exists.
async function refreshMe(token) {
  saveSession({ ...(session() || {}), token });
  const me = await api("/v1/me");
  const ids = me.projects.map((p) => p.id);
  const current = session().project;
  saveSession({ token, member: me.member, organisation: me.organisation, projects: ids,
    project: ids.includes(current) ? current : ids[0] || "" });
  return me;
}

function renderNav(s) {
  document.getElementById("nav").hidden = false;
  const picker = document.getElementById("project");
  picker.replaceChildren(...s.projects.map((p) => el("option", { value: p }, p)));
  picker.value = s.project;
  picker.hidden = s.projects.length < 2;
  const on = s.projects.length === 1 ? `project ${s.project} · ` : "";
  document.getElementById("who").textContent = `${on}${s.member} of ${s.organisation}`;
}

// Routes: #/works[?filters], #/works/<id>, #/troubles[?filters],
// #/assessments[?filters], #/instructions, #/instructions/<name>,
// #/diff/<from>/<to>, #/file/<sha256>?path=<path>.

async function route() {
  const s = session();
  if (!s || !s.member) { login(); return; }
  renderNav(s);
  const [path, query] = (location.hash.slice(1) || "/works").split("?");
  const params = new URLSearchParams(query || "");
  const parts = path.split("/").filter(Boolean).map(decodeURIComponent);
  if (!s.project && parts[0] !== "projects") { location.hash = "#/projects"; return; }
  try {
    if (parts[0] === "projects") await projectsPage();
    else if (parts[0] === "works" && parts[1]) await workPage(parts[1]);
    else if (parts[0] === "instructions" && parts[1]) await instructionPage(parts.slice(1).join("/"));
    else if (parts[0] === "instructions") await instructionsPage();
    else if (parts[0] === "diff" && parts[2]) await diffPage(parts[1], parts[2]);
    else if (parts[0] === "file" && parts[1]) await filePage(parts[1], params.get("path") || parts[1]);
    else if (parts[0] === "troubles") await troublesPage(params);
    else if (parts[0] === "assessments") await assessmentsPage(params);
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

  const troubles = (s) => t.troubles.filter((x) => x.stage === s.stage && x.visit === s.visit);

  show(
    el("p", {}, link("#/works", "← Works")),
    el("h1", {}, w.description),
    el("p", { class: "muted" }, `${w.type} · branch ${w.branch} · started ${when(w.startedAt)} · `, el("span", { class: `status ${w.status}` }, w.status), ` · ${w.id}`),
    el("div", { class: "card" }, el("h2", {}, "The work"),
      general.length > 0 && el("ol", { class: "events" }, general.map(eventItem)),
      assessBox(t)),
    sections.map((s) => el("div", { class: "card", id: `visit-${s.stage}-${s.visit}` },
      el("h2", {}, `${s.stage} · visit ${s.visit}`,
        troubles(s).length > 0 && el("span", { class: "trouble" }, ` ${troubles(s).length} ${troubles(s).length === 1 ? "trouble" : "troubles"}`)),
      (instructions.get(`${s.stage} ${s.visit}`) || []).length > 0 && el("div", { class: "chips" },
        instructions.get(`${s.stage} ${s.visit}`).map((i) => el("span", { class: "chip" },
          `${i.kind} `, link(`#/instructions/${encodeURIComponent(i.name)}`, i.name),
          i.load ? ` (${i.load})` : "", " ", el("span", { class: "hash" }, short(i.version))))),
      artifacts(s).length > 0 && el("p", {}, "Artifacts: ", artifacts(s).map((f, i) => [i ? ", " : "", fileLink(f)])),
      el("ol", { class: "events" }, s.events.map(eventItem)),
      assessBox(t, s.stage, s.visit))),
    el("div", { class: "card" }, el("h2", {}, "Files"),
      t.files.length ? el("ul", {}, t.files.map((f) => el("li", {}, fileLink(f), " ", el("span", { class: "hash muted" }, short(f.sha256)))))
        : el("p", { class: "muted" }, "None synced.")));
}

// Assessments: a member's judgement of the work, or of one stage visit.
// They live only on the server: gf never reads them.

const verdictLabel = (v) => (v === "good" ? "good" : "went wrong");

function assessmentItem(a, { target = false } = {}) {
  const where = a.stage ? `${a.stage} visit ${a.visit}` : "the work";
  return el("li", {},
    el("span", { class: "muted" }, when(a.assessedAt)),
    el("span", {}, el("span", { class: `verdict ${a.verdict}` }, verdictLabel(a.verdict)), ` by ${a.member}`,
      target && [" · ", link(`#/works/${encodeURIComponent(a.work)}`, a.work), ` · ${where}`],
      a.reason ? `: ${a.reason}` : ""));
}

function assessBox(t, stage, visit) {
  const s = session();
  const list = t.assessments.filter((a) => (a.stage || "") === (stage || "") && (a.visit || 0) === (visit || 0));
  const mine = list.find((a) => a.member === s.member);
  const reason = el("textarea", { name: "reason", rows: 2, maxlength: 4000, placeholder: "What went wrong, and why" });
  if (mine && mine.reason) reason.value = mine.reason;
  const status = el("span", { class: "muted", role: "status" });
  const send = async (verdict) => {
    if (verdict === "went-wrong" && !reason.value.trim()) { status.textContent = "Say what went wrong first."; reason.focus(); return; }
    const path = `/works/${encodeURIComponent(t.work.id)}${stage ? `/visits/${encodeURIComponent(stage)}/${visit}` : ""}/assessment`;
    try {
      await api(path, { body: verdict === "good" ? { verdict } : { verdict, reason: reason.value } });
      const y = window.scrollY;
      await workPage(t.work.id);
      window.scrollTo(0, y);
    } catch (err) {
      if (err instanceof Unauthorized) failed(err);
      else status.textContent = `Not saved: ${err.message}`;
    }
  };
  return el("div", { class: "assess" },
    el("h3", {}, stage ? "Assessments of this visit" : "Assessments of the work"),
    list.length ? el("ol", { class: "events" }, list.map((a) => assessmentItem(a))) : el("p", { class: "muted" }, "Nobody has assessed it yet."),
    el("div", { class: "assess-form" },
      reason,
      el("div", { class: "actions" },
        el("button", { type: "button", onclick: () => send("good") }, "Good"),
        el("button", { type: "button", class: "bad", onclick: () => send("went-wrong") }, "Went wrong"),
        mine && el("span", { class: "muted" }, `Yours: ${verdictLabel(mine.verdict)}. Assessing again replaces it.`),
        status)));
}

// filterForm is a page's filters, kept in its route's query.
function filterForm(route, params, fields) {
  const form = el("form", { class: "filters", onsubmit: (e) => {
    e.preventDefault();
    const q = new URLSearchParams();
    for (const [k, v] of new FormData(form)) if (v) q.set(k, v);
    location.hash = `${route}${q.size ? "?" + q : ""}`;
  } },
  fields.map(([name, label, control]) => { control.name = name; return el("label", {}, label, control); }),
  el("button", { type: "submit" }, "Filter"));
  for (const control of form.querySelectorAll("input, select")) control.value = params.get(control.name) || "";
  return form;
}

const troubleTypes = ["AttemptFailed", "AssignmentRejected", "AssignmentEscalated", "ReworkStarted", "WorkReturned", "WorkAbandoned"];
const troubleLabels = { AttemptFailed: "attempt failed", AssignmentRejected: "rejected", AssignmentEscalated: "escalated",
  ReworkStarted: "rework", WorkReturned: "returned", WorkAbandoned: "abandoned" };

async function troublesPage(params) {
  const options = (values) => values.map((v) => el("option", { value: v }, v ? troubleLabels[v] : "any"));
  const form = filterForm("#/troubles", params, [
    ["type", "What", el("select", {}, options(["", ...troubleTypes]))],
    ["stage", "Stage", el("input", { placeholder: "discovery" })],
  ]);
  const troubles = (await api(`/troubles${params.size ? "?" + params : ""}`)).troubles;
  show(el("h1", {}, "Troubles"),
    el("p", { class: "muted" }, "Every moment a work went wrong on its own record, newest first. Open one to assess the visit it happened in."),
    form,
    troubles.length
      ? el("div", { class: "card scroll" }, el("table", {},
        el("thead", {}, el("tr", {}, ["When", "Work", "Stage visit", "What", "Why"].map((h) => el("th", {}, h)))),
        el("tbody", {}, troubles.map((x) => el("tr", {},
          el("td", { class: "muted" }, when(x.occurredAt)),
          el("td", {}, link(`#/works/${encodeURIComponent(x.work)}`, x.work)),
          el("td", {}, x.stage ? `${x.stage} visit ${x.visit}` : "the work"),
          el("td", {}, el("span", { class: "trouble" }, troubleLabels[x.type] || x.type)),
          el("td", {}, x.reason))))))
      : el("p", { class: "muted" }, "No trouble matches."));
}

async function assessmentsPage(params) {
  const form = filterForm("#/assessments", params, [
    ["verdict", "Verdict", el("select", {}, el("option", { value: "" }, "any"), el("option", { value: "good" }, "good"), el("option", { value: "went-wrong" }, "went wrong"))],
    ["stage", "Stage", el("input", { placeholder: "discovery" })],
    ["member", "Member", el("input", {})],
  ]);
  const assessments = (await api(`/assessments${params.size ? "?" + params : ""}`)).assessments;
  show(el("h1", {}, "Assessments"),
    el("p", { class: "muted" }, "Each member's current assessment of each work and stage visit, newest first. They live only on this server."),
    form,
    assessments.length
      ? el("div", { class: "card" }, el("ol", { class: "events" }, assessments.map((a) => assessmentItem(a, { target: true }))))
      : el("p", { class: "muted" }, "No assessment matches."));
}

// Projects: members create them here, and the server makes each one's
// secret, which it shows once and then keeps only as a hash.

async function projectsPage(shown) {
  const s = session();
  const me = await refreshMe(s.token);
  renderNav(session());
  const id = el("input", { name: "id", required: true, pattern: "[a-z0-9][a-z0-9\\-]{0,62}", title: "lowercase letters, digits and dashes", placeholder: "wallets", autocomplete: "off" });
  const status = el("span", { class: "muted", role: "status" });
  const create = el("form", { class: "filters", onsubmit: async (e) => {
    e.preventDefault();
    try {
      const out = await api("/v1/projects", { method: "POST", body: { id: id.value.trim() } });
      await projectsPage({ ...out, created: true });
    } catch (err) {
      if (err instanceof Unauthorized) failed(err);
      else status.textContent = err.message;
    }
  } },
  el("label", {}, "Project id", id),
  el("button", { type: "submit" }, "Create project"),
  status);

  const rows = me.projects.map((p) => {
    const cell = el("td", {});
    const ask = () => cell.replaceChildren(
      el("span", { class: "muted" }, "Every clone will need the new secret. "),
      el("button", { type: "button", class: "bad", onclick: rotate }, "Rotate now"), " ",
      el("button", { type: "button", onclick: idle }, "Keep it"));
    const idle = () => cell.replaceChildren(el("button", { type: "button", onclick: ask }, "Rotate secret"));
    const rotate = async () => {
      try {
        const out = await api(`/v1/projects/${encodeURIComponent(p.id)}/secret`, { method: "POST" });
        await projectsPage(out);
      } catch (err) {
        if (err instanceof Unauthorized) failed(err);
        else cell.replaceChildren(el("span", { class: "error" }, err.message));
      }
    };
    idle();
    return el("tr", {},
      el("td", {}, p.id),
      el("td", { class: "muted" }, `${p.createdBy} · ${when(p.createdAt)}`),
      el("td", { class: "muted" }, `${p.secretBy} · ${when(p.secretAt)}`),
      cell);
  });

  show(el("h1", {}, "Projects"),
    el("p", { class: "muted" }, `Every project of ${me.organisation}. Each syncs with its own secret, which the server shows once, when it makes it.`),
    shown && secretCard(shown),
    el("div", { class: "card" }, el("h2", {}, "New project"),
      el("p", { class: "muted" }, "One per repository. Its id is lowercase letters, digits and dashes, and goes in the repository's .gofast/sync.json."),
      create),
    me.projects.length
      ? el("div", { class: "card scroll" }, el("table", {},
        el("thead", {}, el("tr", {}, ["Project", "Created", "Secret set", ""].map((h) => el("th", {}, h)))),
        el("tbody", {}, rows)))
      : el("p", { class: "muted" }, "No project yet: create the first one above."));
  if (!shown) id.focus();
}

// secretCard shows a project's secret, once, and how a repository syncs
// with it.
function secretCard({ project, secret, created }) {
  const value = el("input", { readonly: true, value: secret, class: "secret", "aria-label": "Project secret" });
  const copied = el("span", { class: "muted", role: "status" });
  const copy = async () => {
    value.select();
    try { await navigator.clipboard.writeText(secret); copied.textContent = "Copied."; } catch { copied.textContent = "Selected: copy it with your keyboard."; }
  };
  const syncJSON = JSON.stringify({ server: location.origin, project }, null, 2);
  return el("div", { class: "card notice" },
    el("h2", {}, created ? `Project ${project} is ready` : `Project ${project} has a new secret`),
    el("p", {}, el("strong", {}, "Copy the secret now: the server keeps only its hash, and won't show it again."),
      created ? "" : " The old one syncs no more: save this one in every clone."),
    el("div", { class: "actions" }, value, el("button", { type: "button", onclick: copy }, "Copy"), copied),
    el("h3", {}, "In the repository"),
    el("ol", { class: "steps" },
      created && el("li", {}, "Commit ", el("code", {}, ".gofast/sync.json"), ":", el("pre", {}, syncJSON)),
      el("li", {}, "Run ", el("code", {}, "gf sync secret"), " and paste the secret. gf keeps it in ", el("code", {}, ".gofast/sync.secret"),
        ", which git ignores: never commit it. Do it once in each clone."),
      el("li", {}, "Run ", el("code", {}, "gf sync"), ".")));
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
  document.getElementById("project").addEventListener("change", (e) => {
    saveSession({ ...session(), project: e.target.value });
    if (location.hash === "#/works") route();
    else location.hash = "#/works";
  });
  route();
});
