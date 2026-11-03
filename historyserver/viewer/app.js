// The gofast history viewer: pages over the history server's JSON API
// (/v1), where a member inspects the projects of their organisation and
// assesses their works and stage visits. The member's token and the
// project they're on live in sessionStorage, for this tab only, and the
// token goes with every request as a bearer token. Every string from the
// history is set as text; only Markdown the server rendered (raw HTML left
// out) is set as HTML. The CSP allows no inline style: anything computed
// is set through the CSSOM (node.style), never a style attribute.
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
// nodes or strings (set as text), or arrays of them, at any depth.
function el(tag, attrs = {}, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v === undefined || v === null || v === false) continue;
    if (k === "class") node.className = v;
    else if (k.startsWith("on")) node.addEventListener(k.slice(2), v);
    else node.setAttribute(k, v === true ? "" : v);
  }
  for (const c of children.flat(Infinity)) {
    if (c === undefined || c === null || c === false) continue;
    node.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return node;
}

// svg builds an SVG element the same way.
function svg(tag, attrs = {}, ...children) {
  const node = document.createElementNS("http://www.w3.org/2000/svg", tag);
  for (const [k, v] of Object.entries(attrs)) node.setAttribute(k, v);
  node.append(...children);
  return node;
}

const link = (href, ...children) => el("a", { href }, ...children);
const short = (hash) => (hash ? hash.slice(0, 12) : "unversioned");
const plural = (n, one, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;
const workHref = (id, stage, visit) => `#/works/${encodeURIComponent(id)}${stage ? `?visit=${encodeURIComponent(stage)}-${visit}` : ""}`;

// Time: when it was, said the way a person would, with the exact moment on
// hover.
const full = (t) => (t ? new Date(t).toLocaleString(undefined, { dateStyle: "full", timeStyle: "medium" }) : "");
const clock = (t) => new Date(t).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
const dayKey = (t) => new Date(t).toDateString();
function dayName(t) {
  const d = new Date(t), today = new Date();
  const yesterday = new Date(today); yesterday.setDate(today.getDate() - 1);
  if (d.toDateString() === today.toDateString()) return "Today";
  if (d.toDateString() === yesterday.toDateString()) return "Yesterday";
  return d.toLocaleDateString(undefined, { weekday: "long", day: "numeric", month: "long", year: d.getFullYear() === today.getFullYear() ? undefined : "numeric" });
}
function ago(t) {
  if (!t) return "";
  const s = (Date.now() - new Date(t)) / 1000;
  if (s < 45) return "just now";
  const steps = [[60, "minute"], [3600, "hour"], [86400, "day"], [604800, "week"], [2629800, "month"], [31557600, "year"]];
  let unit = "second", n = s;
  for (const [secs, name] of steps) { if (s >= secs) { unit = name; n = s / secs; } }
  return new Intl.RelativeTimeFormat(undefined, { numeric: "auto" }).format(-Math.round(n), unit);
}
function lasted(from, to) {
  const m = Math.round((new Date(to) - new Date(from)) / 60000);
  if (m < 1) return "under a minute";
  if (m < 60) return plural(m, "minute");
  const h = Math.floor(m / 60), r = m % 60;
  if (h < 48) return `${h} h${r ? ` ${r} min` : ""}`;
  return plural(Math.round(h / 24), "day");
}
const timeEl = (t, text) => el("time", { datetime: t, title: full(t) }, text);

// avatar is a member's initials on a hue of their own.
function avatar(name, cls = "") {
  let h = 0;
  for (const c of name || "?") h = (h * 31 + c.charCodeAt(0)) % 360;
  const node = el("span", { class: `avatar ${cls}`, "aria-hidden": "true" }, (name || "?").slice(0, 2).toUpperCase());
  node.style.setProperty("--h", String(h));
  return node;
}

// show puts nodes on the page: arrays of them flattened, and false, null,
// undefined and 0 (a skipped "list.length && …") left out.
function show(...nodes) {
  main().replaceChildren(...nodes.flat(Infinity).filter((n) => n !== false && n !== null && n !== undefined && n !== 0));
  window.scrollTo(0, 0);
}

function head({ crumbs, title, lede, facts }) {
  return el("header", { class: "page-head" },
    crumbs && el("nav", { class: "crumbs", "aria-label": "Breadcrumb" }, crumbs.map((c, i) => [i ? el("span", { class: "sep", "aria-hidden": "true" }, "/") : "", c])),
    el("h1", {}, title),
    lede && el("p", { class: "lede" }, lede),
    facts);
}

function empty(title, text, ...extra) {
  return el("div", { class: "panel empty" }, el("h2", {}, title), text && el("p", {}, text), extra);
}

function toast(text, tone = "good") {
  document.querySelector(".toast")?.remove();
  const t = el("div", { class: `toast ${tone}`, role: "status" }, el("span", { class: "dot" }), text);
  document.body.append(t);
  setTimeout(() => t.remove(), 2600);
}

function back() {
  return el("a", { href: "#", onclick: (e) => { e.preventDefault(); history.back(); } }, "Back");
}

function failed(err) {
  if (err instanceof Unauthorized) {
    clearSession();
    login("The server refused the token for this project. Sign in again.");
    return;
  }
  show(head({ title: "This page didn't load" }), empty("The server answered with an error", err.message, el("p", {}, el("button", { type: "button", onclick: route }, "Try again"))));
}

// Login.

function login(message) {
  document.getElementById("nav").hidden = true;
  const token = el("input", { id: "token", name: "token", type: "password", required: true, autocomplete: "current-password", spellcheck: "false" });
  const submit = el("button", { type: "submit", class: "primary" }, "Open the history");
  const form = el("form", { class: "login panel", onsubmit: async (e) => {
    e.preventDefault();
    submit.disabled = true;
    saveSession({ token: token.value });
    try {
      await refreshMe(token.value);
      if (!session().project) location.hash = "#/projects";
      route();
    } catch (err) {
      clearSession();
      login(err instanceof Unauthorized ? "The server doesn't know this token. Check it and try again." : `Couldn't sign in: ${err.message}`);
    }
  } },
  svg("svg", { class: "login-route", viewBox: "0 0 320 44", "aria-hidden": "true" },
    svg("path", { d: "M12 22 H308" }),
    ...[12, 86, 160, 234, 308].map((cx) => svg("circle", { cx, cy: 22, r: 9 }))),
  el("h1", {}, "Sign in"),
  el("p", {}, "Use your member token. It opens every project in your organisation, and this tab keeps it until you close it."),
  message && el("p", { class: "error", role: "alert" }, message),
  el("label", { class: "field", for: "token" }, "Member token"), token,
  submit);
  show(el("div", { class: "login-wrap" }, form));
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

function renderNav(s, section) {
  document.getElementById("nav").hidden = false;
  const picker = document.getElementById("project");
  picker.replaceChildren(...s.projects.map((p) => el("option", { value: p }, p)));
  picker.value = s.project;
  picker.parentElement.hidden = s.projects.length < 2;
  for (const a of document.querySelectorAll(".nav-links a")) {
    if (a.dataset.section === section) a.setAttribute("aria-current", "page");
    else a.removeAttribute("aria-current");
  }
  const on = s.projects.length === 1 ? `${s.project}, ` : "";
  document.getElementById("who").replaceChildren(avatar(s.member, "sm"), el("span", {}, `${on}${s.member} of ${s.organisation}`));
}

// Routes: #/works[?filters], #/works/<id>[?visit=<stage>-<n>],
// #/troubles[?filters], #/assessments[?filters], #/instructions,
// #/instructions/<name>, #/diff/<from>/<to>, #/file/<sha256>?path=<path>.

let routing = 0;
async function route() {
  const s = session();
  if (!s || !s.member) { login(); return; }
  const [path, query] = (location.hash.slice(1) || "/works").split("?");
  const params = new URLSearchParams(query || "");
  const parts = path.split("/").filter(Boolean).map(decodeURIComponent);
  renderNav(s, { diff: "instructions", file: "works" }[parts[0]] || parts[0] || "works");
  if (!s.project && parts[0] !== "projects") { location.hash = "#/projects"; return; }
  review = null;
  const mine = ++routing;
  const progress = document.getElementById("progress");
  const slow = setTimeout(() => { if (mine === routing) progress.hidden = false; }, 120);
  try {
    if (parts[0] === "projects") await projectsPage();
    else if (parts[0] === "works" && parts[1]) await workPage(parts[1], params);
    else if (parts[0] === "instructions" && parts[1]) await instructionPage(parts.slice(1).join("/"));
    else if (parts[0] === "instructions") await instructionsPage();
    else if (parts[0] === "diff" && parts[2]) await diffPage(parts[1], parts[2]);
    else if (parts[0] === "file" && parts[1]) await filePage(parts[1], params.get("path") || parts[1]);
    else if (parts[0] === "troubles") await troublesPage(params);
    else if (parts[0] === "assessments") await assessmentsPage(params);
    else await worksPage(params);
  } catch (err) {
    failed(err);
  } finally {
    clearTimeout(slow);
    if (mine === routing) progress.hidden = true;
  }
}

// works maps each work's id to the work, for pages that name works by id.
async function worksById() {
  return new Map((await api("/works")).works.map((w) => [w.id, w]));
}

// Works.

const workTypes = ["fix-bug", "new-feature", "modify-behaviour", "refactor", "improve-performance"];

async function worksPage(params) {
  const go = (changes) => {
    const q = new URLSearchParams(params);
    for (const [k, v] of Object.entries(changes)) { if (v) q.set(k, v); else q.delete(k); }
    location.hash = `#/works${q.size ? "?" + q : ""}`;
  };
  const q = el("input", { type: "search", name: "q", placeholder: "Search descriptions", value: params.get("q") || "", "aria-label": "Search descriptions" });
  const type = el("select", { name: "type", "aria-label": "Type", onchange: () => go({ type: type.value }) },
    el("option", { value: "" }, "Every type"), workTypes.map((t) => el("option", { value: t }, t)));
  type.value = params.get("type") || "";
  const branch = el("input", { name: "branch", placeholder: "Branch", value: params.get("branch") || "", "aria-label": "Branch", size: 14 });
  const status = params.get("status") || "";
  const statusLink = (value, label) => {
    const q2 = new URLSearchParams(params);
    if (value) q2.set("status", value); else q2.delete("status");
    return el("a", { href: `#/works${q2.size ? "?" + q2 : ""}`, "aria-current": status === value ? "true" : "false" }, label);
  };
  const toolbar = el("form", { class: "toolbar", role: "search", onsubmit: (e) => { e.preventDefault(); go({ q: q.value.trim(), branch: branch.value.trim() }); } },
    el("div", { class: "search grow" }, q),
    el("div", { class: "segmented", role: "group", "aria-label": "Status" },
      statusLink("", "All"), statusLink("active", "In progress"), statusLink("completed", "Completed"), statusLink("abandoned", "Abandoned")),
    type, branch,
    el("button", { type: "submit", class: "sr" }, "Search"));

  const works = (await api(`/works${params.size ? "?" + params : ""}`)).works.slice().reverse();
  const row = (w) => el("a", { class: "row", href: workHref(w.id) },
    el("span", { class: `lamp ${w.status}`, title: w.status }, w.status),
    el("span", { class: "row-title" }, w.description),
    el("span", { class: "row-end" }, el("strong", {}, ago(w.startedAt)), el("span", { class: "hash" }, w.id)),
    el("span", { class: "row-sub" }, el("span", { class: "tag" }, w.type), el("span", { class: "branch" }, w.branch)));
  const group = (title, list) => list.length > 0 && [
    el("h2", { class: "section-title" }, title, el("span", { class: "count" }, String(list.length))),
    el("div", { class: "panel rows" }, list.map(row))];
  const filtered = params.has("q") || params.has("type") || params.has("branch") || params.has("status");
  const active = works.filter((w) => w.status === "active");
  const done = works.filter((w) => w.status !== "active");

  show(head({ title: "Works", lede: `Every piece of work in ${session().project}, newest first. Open one to follow it stage by stage and assess what each stage did.` }),
    toolbar,
    works.length === 0
      ? (filtered ? empty("No work matches", "Loosen a filter, or clear them all.", el("p", {}, link("#/works", "Show every work")))
        : empty("No work synced yet", "Works appear here once a clone runs gf sync."))
      : status ? el("div", { class: "panel rows" }, works.map(row))
        : [group("In progress", active), group("Finished", done)]);
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

// describe says what an event means: [what, tone, why], tone "ok" or "bad"
// when it went well or not, why the reason it carries, set apart.
function describe(e) {
  const p = e.payload || {};
  switch (e.type) {
    case "WorkStarted": return [`Started as ${p.workType} on ${p.branch}`];
    case "WorkCompleted": return ["Work completed", "ok"];
    case "WorkAbandoned": return [`Abandoned by ${owner(p.owner)}`, "bad", p.reason];
    case "StageEntered": return [`Entered ${p.stage}, visit ${p.visit}: ${p.gate} gate, ${plural(p.budget, "attempt")}`];
    case "AssignmentOpened": return [`Opened with ${plural(p.budget, "attempt")}`];
    case "AttemptFailed": return [`Attempt ${p.attempt} failed its checks`, "bad", p.reason];
    case "SubmittedForApproval": return [`Attempt ${p.attempt} submitted for the owner's approval`];
    case "AssignmentRejected": return [`Rejected by ${owner(p.owner)} at attempt ${p.attempt}`, "bad", p.feedback];
    case "AssignmentEscalated": return [`Escalated after ${p.attemptsUsed} of ${plural(p.budget, "attempt")}`, "bad"];
    case "BudgetExtended": return [`${owner(p.owner)} gave ${p.additional} more attempts, ${p.newBudget} in all`];
    case "AssignmentAccepted": {
      const by = e.actor && e.actor.kind === "owner" ? `Approved by ${e.actor.name}` : "Accepted";
      if (p.route) return [`${by}, routed to ${p.route}`, "ok", p.routeReason];
      if (p.stress) return [`${by}, stress testing ${p.stress}`, "ok", p.stressReason];
      return [by, "ok"];
    }
    case "AssignmentCancelled": return ["Cancelled", "", p.reason, "plain"];
    case "AssignmentHandedToFixer": return [`Handed to the fixer at attempt ${p.attempt}`, "bad", p.reason];
    case "FixerEntered": return [`The fixer took over from ${p.from}, visit ${p.fromVisit} (${p.mode})`, "", p.reason, "plain"];
    case "ReworkStarted": return [`The fixer sent the work back to ${p.target}`, "", p.reason, "plain"];
    case "ReworkLimitReached": return [`Rework limit reached after ${plural(p.reworks, "rework")}`, "bad"];
    case "ReworkBudgetExtended": return [`${owner(p.owner)} allowed ${p.additional} more reworks, ${p.newBudget} in all`];
    case "WorkReturned": return [`${owner(p.owner)} sent the work back from ${p.from} to ${p.target}`, "", p.reason, "plain"];
    case "StressDecided": return [`Stress testing ${p.decision}${p.owner ? ` by ${owner(p.owner)}` : ""}`, "", p.reason, "plain"];
    case "BaselineMeasured": return [`Baseline of ${p.nfr} measured`];
    case "WorkFoundNothingToImprove": return ["Nothing to improve: every target was already met", "ok"];
    case "AgentStarted": return [`Agent ${p.agent} started, version ${short(p.sha256)}`];
    case "SkillLoaded": return [`Skill ${p.skill} ${p.load || "invoked"}, version ${short(p.sha256)}`];
  }
  return [`${e.type} ${JSON.stringify(p)}`];
}

// quiet are events the instruction chips already say, or that only
// restate the one before: the record folds them away.
const quiet = new Set(["SkillLoaded", "AgentStarted", "AssignmentOpened"]);

// by says who did it, when the text doesn't already: an agent, or a person
// who isn't the owner named in the text. Automation goes unsaid.
function by(e) {
  const a = e.actor;
  if (!a || !a.kind || a.kind === "automation" || a.kind === "owner") return "";
  return a.name && a.name !== a.kind ? `${a.kind} ${a.name}` : a.kind;
}

// record is a list of events on a small line of its own, with the day said
// once whenever it changes; the quiet ones show on request.
function record(events) {
  const list = el("ol", { class: "record" });
  const render = (all) => {
    let day = "";
    list.replaceChildren(...events.filter((e) => all || !quiet.has(e.type)).flatMap((e) => {
      const [text, tone, why, whyTone] = describe(e);
      const rows = [];
      if (dayKey(e.occurredAt) !== day) {
        day = dayKey(e.occurredAt);
        rows.push(el("li", { class: "day-row" }, el("span", { class: "day" }, dayName(e.occurredAt))));
      }
      const who = by(e);
      rows.push(el("li", { class: quiet.has(e.type) ? "quiet" : "" },
        timeEl(e.occurredAt, clock(e.occurredAt)),
        el("span", { class: `what ${tone || ""} ${whyTone || ""}` }, text, who && el("span", { class: "by" }, who),
          why && el("span", { class: "why" }, why))));
      return rows;
    }));
  };
  render(false);
  const hidden = events.filter((e) => quiet.has(e.type)).length;
  if (!hidden) return list;
  const toggle = el("button", { type: "button", class: "ghost more", "aria-expanded": "false", onclick: () => {
    const all = toggle.getAttribute("aria-expanded") !== "true";
    render(all);
    toggle.setAttribute("aria-expanded", String(all));
    toggle.textContent = all ? "Hide agent and skill loads" : `Show ${plural(hidden, "more event")}: agent and skill loads`;
  } }, `Show ${plural(hidden, "more event")}: agent and skill loads`);
  return el("div", { class: "record-wrap" }, list, toggle);
}

// instructionChips are the agents and skills that instructed a visit, each
// version once: the agent first, then its skills.
function instructionChips(ins) {
  const seen = new Map();
  for (const x of ins) {
    const k = `${x.kind} ${x.name} ${x.version}`;
    if (!seen.has(k)) seen.set(k, x);
  }
  const all = [...seen.values()].sort((a, b) => (a.kind === b.kind ? 0 : a.kind === "agent" ? -1 : 1));
  return el("div", { class: "instructions", "aria-label": "Instructed by" }, all.map((x) =>
    el("a", { class: `instr ${x.kind}`, href: `#/instructions/${encodeURIComponent(x.name)}`, title: `${x.kind} ${x.name}${x.load ? `, ${x.load}` : ""}, version ${short(x.version)}` },
      el("span", { class: `kind ${x.kind}` }, x.kind), el("span", { class: "name" }, x.name),
      el("span", { class: "hash" }, x.version ? x.version.slice(0, 7) : "unversioned"))));
}

// review is the work page's state the keyboard acts on.
let review = null;

async function workPage(id, params = new URLSearchParams(), keep) {
  const t = await api(`/works/${encodeURIComponent(id)}`);
  const w = t.work;
  const me = session().member;
  const sections = [];
  const byKey = new Map();
  const general = [];
  for (const e of t.events) {
    const v = visitOf(w.id, e);
    if (!v) { general.push(e); continue; }
    const key = `${v.stage} ${v.visit}`;
    if (!byKey.has(key)) { byKey.set(key, { ...v, key, events: [] }); sections.push(byKey.get(key)); }
    byKey.get(key).events.push(e);
  }
  const instructions = new Map(t.visits.map((v) => [`${v.stage} ${v.visit}`, v.instructions]));
  const artifacts = (s) => t.files.filter((f) => f.path.split("/").pop().startsWith(`${s.stage}-v${s.visit}.`) || f.path.split("/").pop().startsWith(`${s.stage}-v${s.visit}-`));
  const troubles = (s) => t.troubles.filter((x) => x.stage === s.stage && x.visit === s.visit);
  const assessedBy = (stage, visit) => t.assessments.filter((a) => (a.stage || "") === (stage || "") && (a.visit || 0) === (visit || 0));
  const mineOf = (stage, visit) => assessedBy(stage, visit).find((a) => a.member === me);

  // A visit's state: proceeded (ok), waiting (now), stopped, and whether it
  // had trouble on the way.
  const stateOf = (s, i) => {
    const types = new Set(s.events.map((e) => e.type));
    const state = types.has("AssignmentAccepted") || types.has("WorkFoundNothingToImprove") ? "ok"
      : types.has("AssignmentCancelled") || (w.status === "abandoned" && i === sections.length - 1) ? "stopped"
        : w.status === "active" && i === sections.length - 1 ? "now" : "";
    return { state, trouble: troubles(s).length };
  };
  const outcomeText = { ok: "Proceeded", now: "In progress", stopped: "Stopped", "": "Left" };

  const assessedVisits = sections.filter((s) => mineOf(s.stage, s.visit)).length;
  const meter = el("span");
  const routeMap = el("section", { class: "panel", "aria-label": "Route" },
    el("div", { class: "route-head" },
      el("h2", {}, `${plural(sections.length, "stage visit")}`),
      el("div", { class: "route-progress" },
        el("span", {}, sections.length ? `You've assessed ${assessedVisits} of ${sections.length}` : "Nothing to assess yet"),
        sections.length > 0 && el("span", { class: "meter", role: "progressbar", "aria-valuemin": "0", "aria-valuemax": String(sections.length), "aria-valuenow": String(assessedVisits), "aria-label": "Visits you've assessed" }, meter),
        el("span", { class: "hint" }, el("kbd", {}, "?"), "keys"))),
    el("div", { class: "route" }, el("ol", { class: "stations" }, sections.map((s, i) => {
      const { state, trouble } = stateOf(s, i);
      const mine = mineOf(s.stage, s.visit);
            return el("li", { class: `station ${state} ${trouble ? "trouble" : ""}`, "data-key": s.key },
        el("button", { type: "button", class: "station-btn", onclick: () => focusVisit(i, true),
          "aria-label": `${s.stage}, visit ${s.visit}: ${outcomeText[state].toLowerCase()}${trouble ? `, ${plural(trouble, "trouble")}` : ""}${mine ? `, you said ${mine.verdict === "good" ? "good" : "went wrong"}` : ", not assessed by you"}` },
          el("span", { class: "stop" }),
          el("span", { class: "station-name" }, s.stage),
          el("span", { class: "station-meta" },
            s.visit > 1 && el("span", { class: "again", title: `Visit ${s.visit} of this stage` }, `again ×${s.visit}`),
            trouble > 0 && el("span", { class: "ticks" }, plural(trouble, "trouble"))),
          el("span", { class: `mine ${mine ? mine.verdict : ""}`, title: mine ? `You: ${mine.verdict === "good" ? "good" : "went wrong"}` : "Not assessed by you" },
            mine ? (mine.verdict === "good" ? "✓" : "✕") : "")));
    }))),
    el("div", { class: "legend", "aria-hidden": "true" },
      el("span", {}, el("span", { class: "visit ok" }, el("span", { class: "stop" })), "proceeded"),
      el("span", {}, el("span", { class: "visit trouble ok" }, el("span", { class: "stop" })), "proceeded after trouble"),
      el("span", {}, el("span", { class: "visit now" }, el("span", { class: "stop" })), "in progress"),
      el("span", {}, el("span", { class: "mine" }), "under each stop, your verdict")));

  const visitCards = sections.map((s, i) => {
    const { state, trouble } = stateOf(s, i);
    const ins = instructions.get(s.key) || [];
    const arts = artifacts(s);
    const first = s.events[0], last = s.events[s.events.length - 1];
    return el("article", { class: `panel visit ${state} ${trouble ? "trouble" : ""}`, id: `visit-${s.stage}-${s.visit}`, tabindex: "-1", "data-key": s.key },
      el("header", { class: "visit-head" },
        el("span", { class: "stop" }),
        el("h2", {}, s.stage, el("span", { class: "visit-n" }, s.visit > 1 ? `visit ${s.visit}` : "first visit")),
        el("span", { class: `lamp outcome ${state} ${trouble && !state ? "trouble" : ""}` }, outcomeText[state]),
        el("div", { class: "facts" },
          el("span", { class: "fact" }, timeEl(first.occurredAt, `${dayName(first.occurredAt)}, ${clock(first.occurredAt)}`)),
          s.events.length > 1 && el("span", { class: "fact" }, `took ${lasted(first.occurredAt, last.occurredAt)}`),
          trouble > 0 && el("span", { class: "fact error" }, plural(trouble, "trouble")))),
      el("div", { class: "visit-body" },
        ins.length > 0
          ? instructionChips(ins)
          : el("p", { class: "note" }, "No agent or skill was recorded for this visit. The clone that ran it recorded none: an older gf, or the plugin's hooks weren't loaded."),
        arts.length > 0 && el("div", { class: "artifacts" }, arts.map(artifact)),
        record(s.events),
        assessBox(t, s.stage, s.visit, () => focusVisit(i))));
  });

  const workMine = mineOf("", 0);
  const aside = el("aside", { class: "work-aside" },
    el("section", { class: "panel aside-panel" },
      el("h2", {}, "The work as a whole"),
      assessBox(t)),
    general.length > 0 && el("section", { class: "panel aside-panel" }, el("h2", {}, "Milestones"), record(general)),
    el("section", { class: "panel aside-panel" },
      el("h2", {}, "Files", el("span", { class: "muted" }, t.files.length ? ` ${t.files.length}` : "")),
      t.files.length
        ? el("ul", { class: "files" }, t.files.map((f) => el("li", {}, fileLink(f), el("span", { class: "hash" }, short(f.sha256).slice(0, 7)))))
        : el("p", { class: "muted" }, "None synced.")));

  const copyId = el("button", { type: "button", class: "ghost copy", title: "Copy the work's id", onclick: async () => {
    try { await navigator.clipboard.writeText(w.id); copyId.textContent = "Copied"; } catch { copyId.textContent = w.id; }
  } }, "Copy id");

  show(head({ crumbs: [link("#/works", "Works"), el("span", { class: "hash" }, w.id)], title: w.description,
    facts: el("div", { class: "facts" },
      el("span", { class: `lamp ${w.status}` }, { active: "In progress", completed: "Completed", abandoned: "Abandoned" }[w.status] || w.status),
      el("span", { class: "tag" }, w.type),
      el("span", { class: "branch" }, w.branch),
      el("span", { class: "fact" }, "Started ", timeEl(w.startedAt, ago(w.startedAt))),
      workMine && el("span", { class: `lamp ${workMine.verdict}` }, `You said ${workMine.verdict === "good" ? "good" : "went wrong"}`),
      copyId) }),
    sections.length > 0 && routeMap,
    el("div", { class: "work-layout" },
      el("div", { class: "visits" }, sections.length ? visitCards : empty("No stage visit yet", "The work hasn't entered its first stage.")),
      aside));

  requestAnimationFrame(() => { meter.style.width = `${sections.length ? (100 * assessedVisits) / sections.length : 0}%`; });

  review = { sections, cards: visitCards, index: -1, observer: null };
  watchVisits();
  if (keep) {
    review.lock = true;
    setTimeout(() => { if (review) review.lock = false; }, 500);
    window.scrollTo(0, keep.y);
    if (keep.index >= 0) setCurrent(keep.index);
    if (keep.pop) document.querySelector(`.station[data-key="${CSS.escape(keep.pop)}"] .mine`)?.classList.add("pop");
  } else if (params.get("visit")) {
    const i = sections.findIndex((s) => `${s.stage}-${s.visit}` === params.get("visit"));
    if (i >= 0) focusVisit(i, true);
  }
}

// The visit being reviewed follows the scroll, and the keyboard.

function watchVisits() {
  if (!review || !("IntersectionObserver" in window)) return;
  const seen = new Map();
  review.observer = new IntersectionObserver((entries) => {
    for (const e of entries) seen.set(e.target, e.isIntersecting ? e.boundingClientRect.top : null);
    if (review.lock) return;
    if (atBottom()) { setCurrent(review.cards.length - 1); return; }
    let best = -1, top = Infinity;
    review.cards.forEach((c, i) => { const y = seen.get(c); if (y !== null && y !== undefined && Math.abs(y - 120) < top) { top = Math.abs(y - 120); best = i; } });
    if (best >= 0) setCurrent(best);
  }, { rootMargin: "-80px 0px -45% 0px", threshold: [0, .25] });
  for (const c of review.cards) review.observer.observe(c);
}

const atBottom = () => innerHeight + scrollY >= document.documentElement.scrollHeight - 4;

function setCurrent(i) {
  if (!review) return;
  review.index = i;
  review.cards.forEach((c, j) => c.classList.toggle("current", j === i));
  const stations = document.querySelectorAll(".stations .station");
  stations.forEach((s, j) => s.classList.toggle("current", j === i));
  // Keep the station in sight by scrolling the map sideways only: the
  // page itself must stay where the member scrolled it.
  const station = stations[i], map = station?.closest(".route");
  if (station && map) {
    const left = station.offsetLeft, right = left + station.offsetWidth;
    if (left < map.scrollLeft) map.scrollLeft = left;
    else if (right > map.scrollLeft + map.clientWidth) map.scrollLeft = right - map.clientWidth;
  }
}

function focusVisit(i, scroll = true) {
  if (!review || i < 0 || i >= review.cards.length) return;
  setCurrent(i);
  if (!scroll) return;
  review.lock = true;
  const reduce = matchMedia("(prefers-reduced-motion: reduce)").matches;
  review.cards[i].scrollIntoView({ behavior: reduce ? "auto" : "smooth", block: "start" });
  review.cards[i].focus({ preventScroll: true });
  setTimeout(() => { if (review) review.lock = false; }, 700);
}

function onKey(e) {
  const keys = document.getElementById("keys");
  if (e.key === "Escape") { keys.hidden = true; return; }
  if (e.metaKey || e.ctrlKey || e.altKey || e.target.closest("input, textarea, select, [contenteditable]")) return;
  if (e.key === "?") { keys.hidden = !keys.hidden; return; }
  if (!review || !review.cards.length) return;
  const i = review.index;
  const card = review.cards[i];
  switch (e.key) {
    case "j": focusVisit(Math.min(i + 1, review.cards.length - 1)); break;
    case "k": focusVisit(Math.max(i - 1, 0)); break;
    case "g": card?.querySelector(".verdict-btn.good")?.click(); break;
    case "w": card?.querySelector(".verdict-btn.went-wrong")?.click(); break;
    case "o": {
      const all = card ? [...card.querySelectorAll("details.artifact")] : [];
      const open = all.some((d) => !d.open);
      for (const d of all) d.open = open;
      break;
    }
    default: return;
  }
  e.preventDefault();
}

function fileLink(f) {
  return link(`#/file/${f.sha256}?path=${encodeURIComponent(f.path)}`, f.path.split("/").pop());
}

// artifact is a file read in place: it loads when opened.
function artifact(f) {
  const body = el("div", { class: "reading prose" }, el("p", { class: "muted" }, "Loading…"));
  let loaded = false;
  const box = el("details", { class: "artifact", ontoggle: async () => {
    if (!box.open || loaded) return;
    loaded = true;
    try {
      if (f.path.endsWith(".md")) {
        body.innerHTML = (await api(`/blobs/${f.sha256}/html`)).html; // goldmark's output, raw HTML left out
      } else {
        const pre = el("pre", { class: "reading" }, await api(`/blobs/${f.sha256}`, { text: true }));
        body.replaceWith(pre);
      }
    } catch (err) {
      if (err instanceof Unauthorized) { failed(err); return; }
      loaded = false;
      body.replaceChildren(el("p", { class: "error" }, `Couldn't load it: ${err.message}`));
    }
  } },
  el("summary", {}, el("span", { class: "file" }, f.path.split("/").pop()), el("span", { class: "hash" }, short(f.sha256).slice(0, 7)),
    el("a", { class: "open", href: `#/file/${f.sha256}?path=${encodeURIComponent(f.path)}`, onclick: (e) => e.stopPropagation() }, "Open full page")),
  body);
  return box;
}

// Assessments: a member's judgement of the work, or of one stage visit.
// They live only on the server: gf never reads them.

const verdictLabel = (v) => (v === "good" ? "good" : "went wrong");

function assessmentItem(a, me) {
  return el("li", { class: `assessment ${a.member === me ? "mine-one" : ""}` },
    avatar(a.member),
    el("div", { class: "who-line" },
      el("strong", {}, a.member === me ? "You" : a.member),
      el("span", { class: `lamp ${a.verdict}` }, verdictLabel(a.verdict)),
      timeEl(a.assessedAt, ago(a.assessedAt))),
    a.reason && el("q", {}, a.reason));
}

function assessBox(t, stage, visit, onUse) {
  const s = session();
  const list = t.assessments.filter((a) => (a.stage || "") === (stage || "") && (a.visit || 0) === (visit || 0))
    .sort((a, b) => (a.member === s.member ? -1 : b.member === s.member ? 1 : 0));
  const mine = list.find((a) => a.member === s.member);
  const status = el("span", { class: "status-line", role: "status" });
  const reason = el("textarea", { name: "reason", rows: 3, maxlength: 4000, placeholder: "What should it have done, and what did it do instead?", id: `reason-${stage || "work"}-${visit || 0}` });
  if (mine && mine.reason) reason.value = mine.reason;

  const send = async (verdict) => {
    if (verdict === "went-wrong" && !reason.value.trim()) { status.textContent = "Say what went wrong first."; status.className = "status-line bad"; reason.focus(); return; }
    const path = `/works/${encodeURIComponent(t.work.id)}${stage ? `/visits/${encodeURIComponent(stage)}/${visit}` : ""}/assessment`;
    for (const b of box.querySelectorAll("button")) b.disabled = true;
    status.textContent = "Saving…"; status.className = "status-line";
    try {
      await api(path, { body: verdict === "good" ? { verdict } : { verdict, reason: reason.value } });
      const where = stage ? `${stage}, visit ${visit}` : "the work";
      toast(`Saved: ${where} ${verdict === "good" ? "was good" : "went wrong"}`, verdict);
      await workPage(t.work.id, undefined, { y: window.scrollY, index: review ? review.index : -1, pop: stage ? `${stage} ${visit}` : null });
    } catch (err) {
      if (err instanceof Unauthorized) { failed(err); return; }
      for (const b of box.querySelectorAll("button")) b.disabled = false;
      status.textContent = `Not saved: ${err.message}`; status.className = "status-line bad";
    }
  };

  const form = el("div", { class: "reason-form", hidden: true },
    el("label", { for: reason.id }, "What went wrong"),
    reason,
    el("div", { class: "actions" },
      el("button", { type: "button", class: "danger", onclick: () => send("went-wrong") }, mine && mine.verdict === "went-wrong" ? "Update" : "Save: went wrong"),
      el("button", { type: "button", class: "ghost", onclick: () => { form.hidden = true; wrong.setAttribute("aria-expanded", "false"); status.textContent = ""; } }, "Cancel"),
      el("span", { class: "muted" }, el("kbd", {}, "⌘"), el("kbd", {}, "↵"), " saves")));
  reason.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) { e.preventDefault(); send("went-wrong"); }
    if (e.key === "Escape") { form.hidden = true; e.stopPropagation(); }
  });

  const good = el("button", { type: "button", class: "verdict-btn good", "aria-pressed": mine && mine.verdict === "good" ? "true" : "false",
    onclick: () => { onUse?.(); if (mine && mine.verdict === "good") { status.textContent = "Already yours."; return; } send("good"); } },
  el("span", { class: "glyph", "aria-hidden": "true" }, "✓"), "Good");
  const wrong = el("button", { type: "button", class: "verdict-btn went-wrong", "aria-pressed": mine && mine.verdict === "went-wrong" ? "true" : "false", "aria-expanded": "false",
    onclick: () => { onUse?.(); form.hidden = false; wrong.setAttribute("aria-expanded", "true"); reason.focus(); reason.setSelectionRange(reason.value.length, reason.value.length); } },
  el("span", { class: "glyph", "aria-hidden": "true" }, "✕"), "Went wrong");

  const others = list.filter((a) => a.member !== s.member);
  const box = el("div", { class: "assess" },
    el("div", { class: "assess-head" },
      el("h3", {}, stage ? (mine ? "Your assessment" : "Was this visit right?") : (mine ? "Your assessment" : "Did the work do what it should?")),
      el("div", { class: "verdicts", role: "group", "aria-label": "Your verdict" }, good, wrong)),
    form,
    status,
    (mine || others.length > 0) && el("ul", { class: "assessments" }, (mine ? [mine, ...others] : others).map((a) => assessmentItem(a, s.member))),
    !mine && others.length === 0 && el("p", { class: "muted" }, "Nobody has assessed it yet."),
    mine && el("p", { class: "muted" }, "Assessing again replaces yours."));
  return box;
}

// Feeds: troubles and assessments, grouped by day.

function byDay(items, at, render) {
  const groups = [];
  for (const x of items) {
    const k = dayKey(at(x));
    if (!groups.length || groups[groups.length - 1].k !== k) groups.push({ k, t: at(x), items: [] });
    groups[groups.length - 1].items.push(x);
  }
  return groups.map((g) => el("section", { class: "day-group" },
    el("h2", {}, dayName(g.t), el("span", { class: "muted" }, String(g.items.length))),
    el("ol", { class: "panel feed" }, g.items.map(render))));
}

const troubleTypes = ["AttemptFailed", "AssignmentRejected", "AssignmentEscalated", "ReworkStarted", "WorkReturned", "WorkAbandoned"];
const troubleLabels = { AttemptFailed: "attempt failed", AssignmentRejected: "rejected", AssignmentEscalated: "escalated",
  ReworkStarted: "rework", WorkReturned: "returned", WorkAbandoned: "abandoned" };

// filterBar is a page's filters, kept in its route's query: a select acts
// at once, a text field on Enter.
function filterBar(route, params, fields) {
  const form = el("form", { class: "toolbar", onsubmit: (e) => {
    e.preventDefault();
    const q = new URLSearchParams();
    for (const [k, v] of new FormData(form)) if (v) q.set(k, v);
    location.hash = `${route}${q.size ? "?" + q : ""}`;
  } },
  fields.map(([name, label, control]) => {
    control.name = name;
    control.setAttribute("aria-label", label);
    if (control.tagName === "SELECT") control.addEventListener("change", () => form.requestSubmit());
    return control;
  }),
  params.size > 0 && link(route, "Clear filters"),
  el("button", { type: "submit", class: "sr" }, "Filter"));
  for (const control of form.querySelectorAll("input, select")) control.value = params.get(control.name) || "";
  return form;
}

async function troublesPage(params) {
  const form = filterBar("#/troubles", params, [
    ["type", "What went wrong", el("select", {}, el("option", { value: "" }, "Every kind of trouble"), troubleTypes.map((v) => el("option", { value: v }, troubleLabels[v])))],
    ["stage", "Stage", el("input", { placeholder: "Stage, such as discovery", size: 22 })],
  ]);
  const [troubles, works] = await Promise.all([api(`/troubles${params.size ? "?" + params : ""}`).then((r) => r.troubles), worksById()]);
  const kind = (type) => (troubleLabels[type] || type).replace(" ", "-");
  show(head({ title: "Troubles", lede: "Every moment a work went wrong on its own record, newest first. Open one to read the visit it happened in and assess it." }),
    form,
    troubles.length
      ? byDay(troubles, (x) => x.occurredAt, (x) => el("li", {},
        el("span", { class: `kind-mark ${kind(x.type)}` }, troubleLabels[x.type] || x.type),
        el("div", { class: "target" },
          link(workHref(x.work, x.stage, x.visit), works.get(x.work)?.description || x.work),
          el("span", { class: "where" }, x.stage ? `${x.stage}, visit ${x.visit}` : "the work as a whole"),
          x.reason && el("span", { class: "reason" }, x.reason)),
        timeEl(x.occurredAt, clock(x.occurredAt))))
      : params.size ? empty("No trouble matches", "Try another kind, or another stage.") : empty("No trouble on record", "Every attempt so far passed on its first try."));
}

async function assessmentsPage(params) {
  const form = filterBar("#/assessments", params, [
    ["verdict", "Verdict", el("select", {}, el("option", { value: "" }, "Every verdict"), el("option", { value: "good" }, "Good"), el("option", { value: "went-wrong" }, "Went wrong"))],
    ["stage", "Stage", el("input", { placeholder: "Stage", size: 14 })],
    ["member", "Member", el("input", { placeholder: "Member", size: 14 })],
  ]);
  const [assessments, works] = await Promise.all([api(`/assessments${params.size ? "?" + params : ""}`).then((r) => r.assessments), worksById()]);
  const me = session().member;
  show(head({ title: "Assessments", lede: "Each member's current judgement of each work and stage visit, newest first. They live only on this server: gf never reads them." }),
    form,
    assessments.length
      ? byDay(assessments, (a) => a.assessedAt, (a) => el("li", {},
        el("span", { class: "by" }, avatar(a.member, "sm"), a.member === me ? "You" : a.member),
        el("div", { class: "target" },
          el("span", {}, el("span", { class: `lamp ${a.verdict}` }, verdictLabel(a.verdict)), " ",
            link(workHref(a.work, a.stage, a.visit), works.get(a.work)?.description || a.work)),
          el("span", { class: "where" }, a.stage ? `${a.stage}, visit ${a.visit}` : "the work as a whole"),
          a.reason && el("span", { class: "reason" }, a.reason)),
        timeEl(a.assessedAt, clock(a.assessedAt))))
      : params.size ? empty("No assessment matches", "Try another verdict, stage or member.")
        : empty("Nobody has assessed anything yet", "Open a work and say whether each stage did what it should.", el("p", {}, link("#/works", "Go to the works"))));
}

// Projects: members create them here, and the server makes each one's
// secret, which it shows once and then keeps only as a hash.

async function projectsPage(shown) {
  const s = session();
  const me = await refreshMe(s.token);
  renderNav(session(), "projects");
  const id = el("input", { id: "new-project", name: "id", required: true, pattern: "[a-z0-9][a-z0-9\\-]{0,62}", title: "lowercase letters, digits and dashes", placeholder: "wallets", autocomplete: "off", spellcheck: "false" });
  const status = el("span", { class: "status-line bad", role: "status" });
  const create = el("form", { class: "create", onsubmit: async (e) => {
    e.preventDefault();
    try {
      const out = await api("/v1/projects", { method: "POST", body: { id: id.value.trim() } });
      await projectsPage({ ...out, created: true });
    } catch (err) {
      if (err instanceof Unauthorized) failed(err);
      else status.textContent = err.message;
    }
  } },
  el("div", { class: "field" }, el("label", { for: "new-project" }, "Project id"), id),
  el("button", { type: "submit", class: "primary" }, "Create project"),
  status);

  const cards = me.projects.map((p) => {
    const actions = el("div", { class: "actions" });
    const ask = () => actions.replaceChildren(
      el("span", { class: "muted" }, "Every clone will need the new secret."),
      el("button", { type: "button", class: "danger", onclick: rotate }, "Rotate now"),
      el("button", { type: "button", class: "ghost", onclick: idle }, "Keep it"));
    const open = () => { saveSession({ ...session(), project: p.id }); location.hash = "#/works"; };
    const idle = () => actions.replaceChildren(
      el("button", { type: "button", class: "primary", onclick: open }, session().project === p.id ? "Open works" : "Switch to it"),
      el("button", { type: "button", class: "ghost", onclick: ask }, "Rotate secret"));
    const rotate = async () => {
      try {
        const out = await api(`/v1/projects/${encodeURIComponent(p.id)}/secret`, { method: "POST" });
        await projectsPage(out);
      } catch (err) {
        if (err instanceof Unauthorized) failed(err);
        else actions.replaceChildren(el("span", { class: "error" }, err.message));
      }
    };
    idle();
    return el("article", { class: `panel panel-pad project ${session().project === p.id ? "on" : ""}` },
      el("h2", {}, p.id, session().project === p.id && el("span", { class: "tag" }, "open")),
      el("dl", {},
        el("dt", {}, "Created"), el("dd", {}, `by ${p.createdBy}, `, timeEl(p.createdAt, ago(p.createdAt))),
        el("dt", {}, "Secret"), el("dd", {}, `set by ${p.secretBy}, `, timeEl(p.secretAt, ago(p.secretAt)))),
      actions);
  });

  show(head({ title: "Projects", lede: `Every project of ${me.organisation}. Each syncs with its own secret, which the server shows once, when it makes it.` }),
    shown && secretCard(shown),
    me.projects.length ? el("div", { class: "projects" }, cards) : empty("No project yet", "Create the first one below, then point a repository at it."),
    el("h2", { class: "section-title" }, "New project"),
    el("section", { class: "panel panel-pad stack" },
      el("p", { class: "muted" }, "One per repository. Its id is lowercase letters, digits and dashes, and goes in the repository's .gofast/sync.json."),
      el("div", { class: "create-wrap" }, create)));
  if (!shown && !me.projects.length) id.focus();
}

// secretCard shows a project's secret, once, and how a repository syncs
// with it.
function secretCard({ project, secret, created }) {
  const value = el("input", { readonly: true, value: secret, class: "secret", "aria-label": "Project secret" });
  const copied = el("span", { class: "status-line", role: "status" });
  const copy = async () => {
    value.select();
    try { await navigator.clipboard.writeText(secret); copied.textContent = "Copied."; } catch { copied.textContent = "Selected: copy it with your keyboard."; }
  };
  const syncJSON = JSON.stringify({ server: location.origin, project }, null, 2);
  return el("section", { class: "panel panel-pad secret-card", role: "alert" },
    el("h2", {}, created ? `${project} is ready` : `${project} has a new secret`),
    el("p", {}, el("strong", {}, "Copy the secret now. The server keeps only its hash and won't show it again."),
      created ? "" : " The old one no longer syncs: save this one in every clone."),
    el("div", { class: "secret-row" }, value, el("button", { type: "button", class: "primary", onclick: copy }, "Copy secret"), copied),
    el("ol", { class: "steps" },
      created && el("li", {}, el("div", {}, "Commit ", el("code", {}, ".gofast/sync.json"), ":", el("pre", {}, syncJSON))),
      el("li", {}, el("div", {}, "Run ", el("code", {}, "gf sync secret"), " and paste the secret. gf keeps it in ", el("code", {}, ".gofast/sync.secret"),
        ", which git ignores: never commit it. Do it once in each clone.")),
      el("li", {}, el("div", {}, "Run ", el("code", {}, "gf sync"), "."))));
}

// Skills and agents.

async function instructionsPage() {
  const names = (await api("/instructions")).names;
  show(head({ title: "Skills and agents", lede: "Every skill and agent the history names, and how many versions of each instructed a stage. Open one to see what changed between versions, and where each ran." }),
    names.length
      ? el("div", { class: "panel rows" }, names.map((n) => el("a", { class: "row instr-row", href: `#/instructions/${encodeURIComponent(n.name)}` },
        el("span", { class: `kind ${n.kind}` }, n.kind),
        el("span", { class: "row-title" }, n.name),
        el("span", { class: "row-end" }, el("strong", {}, plural(n.versions, "version")), el("span", {}, "last used ", timeEl(n.lastSeen, ago(n.lastSeen)))),
        el("span", { class: "row-sub" }, el("span", { class: "pips", "aria-hidden": "true" }, Array.from({ length: Math.min(n.versions, 16) }, () => el("i")))))))
      : empty("No skill or agent recorded yet", "They appear once a synced visit records which instructed it."));
}

async function instructionPage(name) {
  const [it, works] = await Promise.all([api(`/instructions?name=${encodeURIComponent(name)}`), worksById()]);
  // A visit that loaded it more than once is still one use.
  const usesOf = (v) => [...new Map(v.uses.map((u) => [`${u.work} ${u.stage} ${u.visit}`, u])).values()];
  const versioned = it.versions.filter((v) => v.version);
  const newest = it.versions.slice().reverse();
  show(head({ crumbs: [link("#/instructions", "Skills and agents"), it.kind], title: it.name,
    lede: `${plural(versioned.length, "version")} of this ${it.kind}, newest first.` }),
  el("ol", { class: "versions" }, newest.map((v, n) => {
    const i = versioned.indexOf(v);
    const previous = i > 0 ? versioned[i - 1] : null;
    return el("li", { class: `version ${n === 0 ? "latest" : ""}` },
      el("span", { class: "stop" }),
      el("div", { class: "panel panel-pad" },
        el("div", { class: "version-head" },
          el("h2", {}, short(v.version)),
          n === 0 && el("span", { class: "tag" }, "latest"),
          el("span", { class: "muted" }, "first used ", timeEl(v.firstSeen, ago(v.firstSeen))),
          previous && el("a", { class: "button", href: `#/diff/${previous.version}/${v.version}` }, `Compare with ${short(previous.version).slice(0, 7)}`)),
        v.files.length > 0 && el("div", { class: "facts" }, v.files.map((f) => fileLink(f))),
        !v.version && el("p", { class: "note" }, "Loaded without a version: another plugin's skill, or gf didn't know where the plugin was."),
        el("ul", { class: "uses" }, usesOf(v).map((u) => el("li", {},
          link(workHref(u.work, u.stage, u.visit), works.get(u.work)?.description || u.work),
          el("span", { class: "tag" }, `${u.stage}, visit ${u.visit}`), u.load && el("span", { class: "muted" }, u.load),
          el("span", { class: "muted" }, timeEl(u.at, ago(u.at))))))));
  })));
}

// diffLines lays a unified diff out on two gutters, old and new line
// numbers, read from each hunk's header.
function diffLines(unified) {
  const cells = [];
  let a = 0, b = 0;
  const lines = unified.split("\n");
  if (lines[lines.length - 1] === "") lines.pop();
  for (const l of lines) {
    if (l.startsWith("---") || l.startsWith("+++")) continue;
    const h = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@(.*)/.exec(l);
    if (h) { a = Number(h[1]); b = Number(h[2]); cells.push(el("span", { class: "hunk" }, l)); continue; }
    const sign = l[0] === "+" ? "+" : l[0] === "-" ? "-" : " ";
    const cls = sign === "+" ? "add" : sign === "-" ? "del" : "";
    cells.push(el("span", { class: `n ${cls}` }, sign === "+" ? "" : String(a)),
      el("span", { class: `n ${cls}` }, sign === "-" ? "" : String(b)),
      el("span", { class: `s ${cls}` }, sign === " " ? "" : sign),
      el("span", { class: cls }, l.slice(1) || " "));
    if (sign !== "+") a++;
    if (sign !== "-") b++;
  }
  return el("div", { class: "diff" }, cells);
}

async function diffPage(from, to) {
  const d = await api(`/instructions/${from}/diff/${to}`);
  const count = (f, c) => (f.unified || "").split("\n").filter((l) => l.startsWith(c) && !l.startsWith(c.repeat(3))).length;
  show(head({ crumbs: [back(), link("#/instructions", "Skills and agents")],
    title: el("span", { class: "compare" }, short(from), el("span", { class: "arrow", "aria-label": "to" }), short(to)),
    lede: d.files.length ? `What changed in ${plural(d.files.length, "file")}.` : "Nothing changed between these versions." }),
  d.files.map((f) => el("section", { class: "panel diff-file" },
    el("header", {}, el("strong", {}, f.path), el("span", { class: `tag ${f.status}` }, f.status),
      f.unified && el("span", { class: "muted" }, `+${count(f, "+")} −${count(f, "-")}`)),
    f.unified ? el("div", { class: "diff-scroll" }, diffLines(f.unified)) : el("p", { class: "panel-pad muted" }, "No text to compare."))));
}

// A file: Markdown rendered by the server, anything else as text.

async function filePage(sha, path) {
  const name = path.split("/").pop();
  const header = head({ crumbs: [back(), el("span", { class: "hash" }, path.slice(0, -name.length) || "/")], title: name,
    facts: el("div", { class: "facts" }, el("span", { class: "hash", title: sha }, `sha256 ${short(sha)}`)) });
  if (path.endsWith(".md")) {
    const rendered = await api(`/blobs/${sha}/html`);
    const body = el("article", { class: "prose" });
    body.innerHTML = rendered.html; // goldmark's output, raw HTML left out
    show(header, el("div", { class: "panel reader" }, body));
    return;
  }
  show(header, el("pre", {}, await api(`/blobs/${sha}`, { text: true })));
}

window.addEventListener("hashchange", route);
window.addEventListener("keydown", onKey);
// At the bottom of a work, the last visit is the one being read, though it
// can't scroll up to where the observer looks.
window.addEventListener("scroll", () => {
  if (!review || review.lock || !review.cards.length || !review.cards[0].isConnected) return;
  if (atBottom()) setCurrent(review.cards.length - 1);
}, { passive: true });
window.addEventListener("DOMContentLoaded", () => {
  document.getElementById("logout").addEventListener("click", () => { clearSession(); location.hash = ""; login(); });
  document.getElementById("keys").addEventListener("click", (e) => { if (e.target.id === "keys") e.currentTarget.hidden = true; });
  document.getElementById("project").addEventListener("change", (e) => {
    saveSession({ ...session(), project: e.target.value });
    if (location.hash === "#/works") route();
    else location.hash = "#/works";
  });
  route();
});
