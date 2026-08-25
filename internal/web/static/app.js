// whodar web UI: an ask view over /api/ask and directory views over
// /api/directory, routed by location.hash. Result text is set with
// textContent so indexed data cannot inject markup.

const form = document.getElementById("ask-form");
const qInput = document.getElementById("q");
const modeSeg = document.getElementById("mode-seg");
const statusEl = document.getElementById("status");
const summaryEl = document.getElementById("summary");
const peopleSection = document.getElementById("people-section");
const channelsSection = document.getElementById("channels-section");
const peopleEl = document.getElementById("people");
const channelsEl = document.getElementById("channels");
const button = form.querySelector("button");
const examplesEl = document.getElementById("examples");
const askView = document.getElementById("view-ask");
const dirView = document.getElementById("view-directory");
const recallView = document.getElementById("view-recall");
const recallForm = document.getElementById("recall-form");
const recallQuery = document.getElementById("recall-q");
const recallMe = document.getElementById("recall-me");
const recallStatus = document.getElementById("recall-status");
const recallList = document.getElementById("recall-list");
const recallScope = document.getElementById("recall-scope");
const recallSince = document.getElementById("recall-since");
const recallSources = document.getElementById("recall-sources");
const recallSlabel = document.getElementById("recall-slabel");
const recallCount = document.getElementById("recall-count");
const expView = document.getElementById("view-exposure");
const expStatus = document.getElementById("exp-status");
const expRisk = document.getElementById("exp-risk");
const expDrift = document.getElementById("exp-drift");
const expRegions = document.getElementById("exp-regions");
const expSpans = document.getElementById("exp-spans");
const expDepInput = document.getElementById("exp-dep-input");
const expDepGo = document.getElementById("exp-dep-go");
const expDepResult = document.getElementById("exp-dep-result");
const expDepPeople = document.getElementById("exp-dep-people");
const expProofGo = document.getElementById("exp-proof-go");
const expProofOut = document.getElementById("exp-proof-out");
const cliView = document.getElementById("view-cli");
const cliTabs = document.getElementById("cli-tabs");
const cliOut = document.getElementById("cli-out");
const cliStatus = document.getElementById("cli-status");
const cliNote = document.getElementById("cli-note");
// The commands the demo can print, in the order somebody would run them.
const CLI_COMMANDS = [
  { cmd: "ask", label: "whodar ask" },
  { cmd: "ask-llm", label: "whodar ask --mode llm", recorded: true },
  { cmd: "risk", label: "whodar risk" },
  { cmd: "ownership", label: "whodar ownership" },
  { cmd: "related", label: "whodar related" },
  { cmd: "attest", label: "whodar attest" },
];
let cliCurrent = "";
let recallData = null;
let recallSourceFilter = null;
let recallTimer = null;
const dirTitle = document.getElementById("dir-title");
const dirFilter = document.getElementById("dir-filter");
const dirStatus = document.getElementById("dir-status");
const dirList = document.getElementById("dir-list");
const sideNav = document.getElementById("side-nav");
const facetTeam = document.getElementById("facet-team");
const facetOrg = document.getElementById("facet-org");

// The active answer mode and AI provider, driven by the segmented controls.
let currentMode = "keyword";
let currentProvider = "ollama";

// providerTouched blocks the server default from stomping a user's pick.
let providerTouched = false;

// modesReport holds readiness from /api/modes: modes, providers, provider.
let modesReport = { modes: {}, providers: {} };

const providerSeg = document.getElementById("provider-seg");

// PROVIDER_LABELS names providers the way people know them.
const PROVIDER_LABELS = {
  ollama: "the local model", anthropic: "Claude", openai: "ChatGPT", gemini: "Gemini",
};

modeSeg.addEventListener("click", (event) => {
  const btn = event.target.closest(".seg-btn");
  if (!btn) return;
  currentMode = btn.dataset.mode;
  for (const b of modeSeg.querySelectorAll(".seg-btn")) {
    b.classList.toggle("active", b === btn);
    b.setAttribute("aria-pressed", String(b === btn));
  }
  providerSeg.hidden = currentMode !== "llm";
  showModeHint();
  if (currentMode !== "keyword") loadModes().then(showModeHint);
});

providerSeg.addEventListener("click", (event) => {
  const btn = event.target.closest(".seg-btn");
  if (!btn) return;
  currentProvider = btn.dataset.provider;
  providerTouched = true;
  syncProviderButtons();
  showModeHint();
  loadModes().then(showModeHint);
});

// syncProviderButtons marks the active provider button.
function syncProviderButtons() {
  for (const b of providerSeg.querySelectorAll(".seg-btn")) {
    const active = b.dataset.provider === currentProvider;
    b.classList.toggle("active", active);
    b.setAttribute("aria-pressed", String(active));
  }
}

// providerInfo is the readiness of the currently selected AI provider.
function providerInfo() {
  return (modesReport.providers || {})[currentProvider] || (modesReport.modes || {}).llm;
}

// loadModes refreshes readiness, marks unready modes and providers with a
// dot, and upgrades tooltips with the specific guidance.
async function loadModes() {
  try {
    const res = await fetch("/api/modes");
    if (!res.ok) return;
    modesReport = await res.json();
  } catch (err) {
    return;
  }
  if (!providerTouched && modesReport.provider) {
    currentProvider = modesReport.provider;
    syncProviderButtons();
  }
  const modes = modesReport.modes || {};
  for (const b of modeSeg.querySelectorAll(".seg-btn")) {
    const info = b.dataset.mode === "llm" ? providerInfo() : modes[b.dataset.mode];
    if (!info) continue;
    b.classList.toggle("warn", info.ready === false);
    // The AI button keeps its generic tooltip; provider buttons carry the
    // provider-specific guidance.
    if (info.hint && b.dataset.mode !== "llm") b.dataset.tip = info.hint;
  }
  const providers = modesReport.providers || {};
  for (const b of providerSeg.querySelectorAll(".seg-btn")) {
    const info = providers[b.dataset.provider];
    if (!info) continue;
    b.classList.toggle("warn", info.ready === false);
    if (info.hint) b.dataset.tip = info.hint;
  }
}

// showModeHint puts the selected mode or provider's guidance on the status
// line, so picking AI without a model tells you what to do before you ask.
function showModeHint() {
  const info = currentMode === "llm" ? providerInfo() : (modesReport.modes || {})[currentMode];
  if (info && info.hint) statusEl.textContent = info.hint;
}

// showExamples offers a few questions this index can actually answer, taken
// from the subjects the most people work on. Hardcoded examples go stale the
// moment they name something the index does not have, and a suggested question
// that returns nothing is the worst possible first impression.
async function showExamples() {
  let subjects = [];
  try {
    const dir = await directory();
    subjects = (dir.topics || []).slice(0, 3).map((t) => String(t.name).replace(/-/g, " "));
  } catch (err) {
    // Fall through to the generic prompts below.
  }
  const queries = subjects.length
    ? subjects.map((s, i) => (i === 0 ? "who do I talk to about " + s : "who knows " + s))
    : ["who do I talk to about billing", "who knows kubernetes", "vacation policy"];
  examplesEl.replaceChildren();
  for (const example of queries) {
    const b = el("button", "example", example);
    b.type = "button";
    b.addEventListener("click", () => runAsk(example));
    examplesEl.appendChild(b);
  }
}

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  await ask();
});

async function ask() {
  const q = qInput.value.trim();
  if (!q) return;

  button.disabled = true;
  examplesEl.hidden = true;
  clearResults();
  statusEl.textContent =
    currentMode === "llm" ? "Asking " + (PROVIDER_LABELS[currentProvider] || "the model") + "..."
    : currentMode === "semantic" ? "Searching by meaning..."
    : "Searching...";

  try {
    const params = new URLSearchParams({ q, mode: currentMode });
    if (currentMode === "llm" && currentProvider) params.set("provider", currentProvider);
    setParam("person", "");
    setParam("q", q);
    const res = await fetch("/api/ask?" + params.toString());
    const data = await res.json();
    if (!res.ok) {
      statusEl.textContent = "Error: " + (data.error || res.statusText);
      return;
    }
    render(data);
  } catch (err) {
    statusEl.textContent = "Request failed: " + err.message;
  } finally {
    button.disabled = false;
  }
}

// runAsk switches to the ask view and runs the query.
function runAsk(q) {
  qInput.value = q;
  if (location.hash && location.hash !== "#/") location.hash = "#/";
  showView("ask");
  ask();
}

// setParam updates one query parameter in the address bar without reloading.
function setParam(key, val) {
  const p = new URLSearchParams(location.search);
  if (val) {
    p.set(key, val);
  } else {
    p.delete(key);
  }
  const s = p.toString();
  history.replaceState(null, "", (s ? "?" + s : location.pathname) + location.hash);
}

function clearResults() {
  summaryEl.hidden = true;
  summaryEl.textContent = "";
  peopleEl.replaceChildren();
  channelsEl.replaceChildren();
  peopleSection.hidden = true;
  channelsSection.hidden = true;
}

function render(data) {
  if (data.summary) {
    summaryEl.textContent = data.summary;
    summaryEl.hidden = false;
  }
  const people = data.people || [];
  const channels = data.channels || [];

  people.forEach((p, i) => peopleEl.appendChild(personCard(p, data.query, i)));
  channels.forEach((c, i) => channelsEl.appendChild(channelCard(c, data.query, i)));
  peopleSection.hidden = people.length === 0;
  channelsSection.hidden = channels.length === 0;

  if (people.length === 0 && channels.length === 0) {
    statusEl.textContent =
      "No matches. Try fewer or different words, or browse Topics for what the index knows.";
  } else {
    statusEl.textContent = people.length + " people, " + channels.length + " channels";
  }
}

function el(tag, cls, text) {
  const node = document.createElement(tag);
  if (cls) node.className = cls;
  if (text != null) node.textContent = text;
  return node;
}

function chips(parent, items) {
  if (!items || !items.length) return;
  const wrap = el("div", "chips");
  for (const item of items) wrap.appendChild(el("span", "chip", item));
  parent.appendChild(wrap);
}

function confidenceBadge(c) {
  if (!c) return null;
  const label = c >= 0.75 ? "strong" : c >= 0.45 ? "moderate" : "weak";
  return el("span", "conf conf-" + label, label);
}

function voteButtons(query, target) {
  const wrap = el("div", "votes");
  const note = document.createElement("input");
  note.className = "vote-note";
  note.type = "text";
  note.placeholder = "why? (optional)";
  for (const [label, vote] of [["helpful", "helpful"], ["wrong", "not-helpful"]]) {
    const button = el("button", "vote", label);
    button.type = "button";
    button.addEventListener("click", async () => {
      try {
        const res = await fetch("/api/feedback", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ query, vote, comment: note.value.trim(), ...target }),
        });
        wrap.replaceChildren(el("span", "voted", res.ok ? "thanks" : "failed"));
      } catch (err) {
        wrap.replaceChildren(el("span", "voted", "failed"));
      }
    });
    wrap.appendChild(button);
  }
  wrap.appendChild(note);
  return wrap;
}

// rankBadge renders the zero-padded position marker in a result card corner.
function rankBadge(i) {
  return el("span", "rank", String(i + 1).padStart(2, "0"));
}

function personCard(p, query, i) {
  const card = el("div", "card");
  card.appendChild(rankBadge(i));
  const name = el("div", "name");
  const toggle = el("button", "name-toggle", p.name || p.email || "unknown");
  toggle.type = "button";
  toggle.title = "Show everything whodar knows";
  toggle.addEventListener("click", () => openProfile(p.id || p.email));
  name.appendChild(toggle);
  const copyText = ((p.name || "") + (p.email ? " <" + p.email + ">" : "")).trim();
  if (copyText) name.appendChild(copyButton(copyText));
  const badge = confidenceBadge(p.confidence);
  if (badge) name.appendChild(badge);
  card.appendChild(name);

  const sub = [p.title, p.team].filter(Boolean).join(" · ");
  if (sub) card.appendChild(el("div", "sub", sub));
  if (p.email) card.appendChild(el("div", "sub", p.email));
  chips(card, p.reasons);
  if (query && p.id) card.appendChild(voteButtons(query, { person: p.id }));
  return card;
}

async function openProfile(id) {
  if (!id) return;
  try {
    const res = await fetch("/api/person?id=" + encodeURIComponent(id));
    if (!res.ok) return;
    showProfile(await res.json());
  } catch (err) {
    // A failed lookup just leaves the page as it is.
  }
}

function safeHref(url) {
  var s = String(url == null ? "" : url).trim();
  return /^(https?:|mailto:)/i.test(s) ? s : "#";
}
function showProfile(p) {
  closeProfile();
  const backdrop = el("div", "modal-backdrop");
  backdrop.id = "profile-modal";
  backdrop.addEventListener("click", (event) => {
    if (event.target === backdrop) closeProfile();
  });
  const modal = el("div", "modal");

  const name = el("div", "name", p.name || p.id);
  const close = el("button", "modal-close", "close");
  close.type = "button";
  close.addEventListener("click", closeProfile);
  name.appendChild(close);
  modal.appendChild(name);

  const sub = [p.title, p.team, p.org].filter(Boolean).join(" · ");
  if (sub) modal.appendChild(el("div", "sub", sub));

  const rows = el("div", "details");
  const row = (label, value) => {
    const r = el("div", "detail-row");
    r.appendChild(el("span", "detail-label", label));
    r.appendChild(value);
    rows.appendChild(r);
  };
  if (p.email) {
    const v = el("span", "detail-value", p.email);
    v.appendChild(copyButton(p.email));
    const mail = el("a", "detail-action", "email");
    mail.href = "mailto:" + p.email;
    v.appendChild(mail);
    row("Email", v);
  }
  if (p.id && p.id !== p.email) row("Id", el("span", "detail-value", p.id));
  if (p.identities && p.identities.length) {
    row("Also known as", el("span", "detail-value", p.identities.join(", ")));
  }
  if (p.joins && p.joins.length) {
    const v = el("span", "detail-value detail-joins");
    for (const j of p.joins) {
      const line = el("div", "join-line");
      line.appendChild(el("span", "join-alias", j.alias));
      const pct = Math.round((j.confidence || 0) * 100);
      const bar = el("span", "join-bar");
      const fill = el("span", "join-bar-fill");
      fill.style.width = pct + "%";
      bar.appendChild(fill);
      line.appendChild(bar);
      line.appendChild(el("span", "join-pct", pct + "%"));
      line.appendChild(el("span", "join-reason", j.reason || ""));
      v.appendChild(line);
    }
    row("Merged by", v);
  }
  if (p.manager && (p.manager.name || p.manager.email)) {
    row("Manager", el("span", "detail-value", p.manager.name || p.manager.email));
  }
  if (p.channels && p.channels.length) {
    row("Active in", el("span", "detail-value", p.channels.map((c) => "#" + c).join(", ")));
  }
  if (p.topics && p.topics.length) {
    const v = el("span", "detail-value detail-chips");
    for (const topic of p.topics) v.appendChild(el("span", "chip", topic));
    row("Knows about", v);
  }
  modal.appendChild(rows);
  backdrop.appendChild(modal);
  document.body.appendChild(backdrop);
  setParam("person", p.id);
}

function closeProfile() {
  const open = document.getElementById("profile-modal");
  if (open) {
    open.remove();
    setParam("person", "");
  }
}

document.addEventListener("keydown", (event) => {
  if (event.key === "Escape") closeProfile();
  const tag = document.activeElement && document.activeElement.tagName;
  if (event.key === "/" && tag !== "INPUT" && tag !== "TEXTAREA" && tag !== "SELECT") {
    event.preventDefault();
    (currentView === "ask" ? qInput : dirFilter).focus();
  }
});

function channelCard(c, query, i) {
  const card = el("div", "card");
  card.appendChild(rankBadge(i));
  const name = el("div", "name", "#" + c.name);
  name.appendChild(copyButton("#" + c.name));
  const badge = confidenceBadge(c.confidence);
  if (badge) name.appendChild(badge);
  card.appendChild(name);
  if (c.topic) card.appendChild(el("div", "sub", c.topic));

  const members = c.members || [];
  if (members.length) {
    const sub = el("div", "sub");
    sub.appendChild(document.createTextNode("Active: "));
    members.forEach((m, i) => {
      if (i) sub.appendChild(document.createTextNode(", "));
      const span = el("span", "member", m.name || m.email || "");
      if (m.email) span.title = m.email;
      sub.appendChild(span);
    });
    card.appendChild(sub);
  }
  if (c.url) {
    const link = el("a", "card-open", "Open in Slack");
    link.href = safeHref(c.url);
    link.target = "_blank";
    link.rel = "noopener";
    card.appendChild(link);
  }
  chips(card, c.reasons);
  if (query && c.name) card.appendChild(voteButtons(query, { channel: c.name }));
  return card;
}

function copyButton(text) {
  const button = el("button", "copy", "copy");
  button.type = "button";
  button.addEventListener("click", async () => {
    try {
      await navigator.clipboard.writeText(text);
      button.textContent = "copied";
      setTimeout(() => (button.textContent = "copy"), 1200);
    } catch (err) {
      button.textContent = "failed";
    }
  });
  return button;
}

// Directory views: one fetch, cached for the page's life, filtered client side.

const DIR_VIEWS = {
  people: { title: "People", empty: "No people indexed yet." },
  channels: { title: "Channels", empty: "No channels indexed yet." },
  teams: { title: "Teams", empty: "No teams indexed yet." },
  topics: { title: "Topics", empty: "No topics indexed yet." },
};

let dirPromise = null;

// directory fetches /api/directory once and caches the result.
function directory() {
  if (!dirPromise) {
    dirPromise = fetch("/api/directory").then((res) => {
      if (!res.ok) throw new Error(res.statusText);
      return res.json();
    });
    dirPromise.catch(() => (dirPromise = null));
  }
  return dirPromise;
}

let currentView = "ask";

// pendingTeamFacet carries a team chosen in the Teams view into the People
// facet on the next render.
let pendingTeamFacet = "";

// viewFromHash maps the location hash to a view name.
function viewFromHash() {
  const name = location.hash.replace(/^#\//, "");
  if (name === "recall" && recallView) return "recall";
  if (name === "exposure" && expView) return "exposure";
  if (name === "cli" && cliView) return "cli";
  return DIR_VIEWS[name] ? name : "ask";
}

// showView flips between the ask view and a directory view and marks the nav.
function showView(view) {
  currentView = view;
  askView.hidden = view !== "ask";
  dirView.hidden = view === "ask" || view === "recall" || view === "exposure" || view === "cli";
  if (recallView) recallView.hidden = view !== "recall";
  if (expView) expView.hidden = view !== "exposure";
  if (cliView) cliView.hidden = view !== "cli";
  for (const a of sideNav.querySelectorAll("a")) {
    a.classList.toggle("active", a.dataset.view === view);
  }
  facetTeam.hidden = view !== "people";
  facetOrg.hidden = view !== "people";
  if (view === "recall") {
    openRecall();
    return;
  }
  if (view === "exposure") {
    renderExposure();
    return;
  }
  if (view === "cli") {
    const want = new URLSearchParams(location.search).get("cmd");
    renderCLI(want || cliCurrent || CLI_COMMANDS[0].cmd);
    return;
  }
  if (view !== "ask") {
    dirTitle.textContent = DIR_VIEWS[view].title;
    dirFilter.placeholder = "Filter " + view + "...";
    renderDirectory(view);
  }
}

// fillFacets populates the team and org dropdowns from the people directory,
// once, and applies any team picked from the Teams view.
function fillFacets(people) {
  if (!facetTeam.options.length) {
    fillFacet(facetTeam, "All teams", people.map((p) => p.team));
    fillFacet(facetOrg, "All orgs", people.map((p) => p.org));
  }
  if (pendingTeamFacet) {
    facetTeam.value = pendingTeamFacet;
    if (facetTeam.value !== pendingTeamFacet) facetTeam.value = "";
    pendingTeamFacet = "";
  }
}

// fillFacet fills one dropdown with an all option plus sorted unique values.
function fillFacet(sel, allLabel, values) {
  const all = document.createElement("option");
  all.value = "";
  all.textContent = allLabel;
  sel.appendChild(all);
  const unique = [...new Set(values.filter(Boolean))].sort((a, b) => a.localeCompare(b));
  for (const v of unique) {
    const opt = document.createElement("option");
    opt.value = v;
    opt.textContent = v;
    sel.appendChild(opt);
  }
}

// fillNavCounts writes entity counts into the sidebar once the directory
// loads. A failed load just leaves the nav plain.
async function fillNavCounts() {
  try {
    const dir = await directory();
    for (const span of document.querySelectorAll(".nav-count")) {
      span.textContent = (dir[span.dataset.count] || []).length;
    }
  } catch (err) {
    // The nav works without counts.
  }
}

async function renderDirectory(view) {
  dirStatus.textContent = "Loading...";
  dirList.replaceChildren();
  let dir;
  try {
    dir = await directory();
  } catch (err) {
    dirStatus.textContent = "Could not load the directory: " + err.message;
    return;
  }
  if (currentView !== view) return;

  const rows = dir[view] || [];
  if (view === "people") fillFacets(rows);
  const q = dirFilter.value.trim().toLowerCase();
  let shown = q ? rows.filter((r) => rowText(r).includes(q)) : rows;
  if (view === "people") {
    if (facetTeam.value) shown = shown.filter((r) => r.team === facetTeam.value);
    if (facetOrg.value) shown = shown.filter((r) => r.org === facetOrg.value);
  }

  dirList.replaceChildren();
  for (const r of shown) dirList.appendChild(directoryRow(view, r));
  if (rows.length === 0) {
    dirStatus.textContent = DIR_VIEWS[view].empty;
  } else if (shown.length !== rows.length) {
    dirStatus.textContent = shown.length + " of " + rows.length;
  } else {
    dirStatus.textContent = String(rows.length);
  }
}

// rowText flattens a directory row for filtering.
function rowText(r) {
  return [r.name, r.email, r.title, r.team, r.topic, r.org, ...(r.topics || [])]
    .filter(Boolean)
    .join(" ")
    .toLowerCase();
}

function directoryRow(view, r) {
  switch (view) {
    case "people":
      return dirPersonRow(r);
    case "channels":
      return dirChannelRow(r);
    case "teams":
      return dirTeamRow(r);
    default:
      return dirTopicRow(r);
  }
}

function dirPersonRow(p) {
  const card = el("div", "card");
  const name = el("div", "name");
  const toggle = el("button", "name-toggle", p.name || p.id);
  toggle.type = "button";
  toggle.title = "Show everything whodar knows";
  toggle.addEventListener("click", () => openProfile(p.id));
  name.appendChild(toggle);
  card.appendChild(name);
  const sub = [p.title, p.team, p.org].filter(Boolean).join(" · ");
  if (sub) card.appendChild(el("div", "sub", sub));
  if (p.email) card.appendChild(el("div", "sub", p.email));
  chips(card, p.topics);
  return card;
}

function dirChannelRow(c) {
  const card = el("div", "card");
  const name = el("div", "name", "#" + c.name);
  name.appendChild(copyButton("#" + c.name));
  card.appendChild(name);
  if (c.topic) card.appendChild(el("div", "sub", c.topic));
  card.appendChild(el("div", "sub", c.members + " active " + (c.members === 1 ? "person" : "people")));
  return card;
}

// activate makes a whole card act as one button: click or Enter/Space anywhere
// on it fires the action, not just an inner label.
function activate(node, fn) {
  node.addEventListener("click", fn);
  node.addEventListener("keydown", (e) => {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      fn();
    }
  });
}

function dirTeamRow(t) {
  const card = el("div", "card dir-clickable");
  card.setAttribute("role", "button");
  card.tabIndex = 0;
  card.title = "Show this team's people";
  card.appendChild(el("div", "name", t.name));
  const sub = [t.org, t.people + (t.people === 1 ? " person" : " people")].filter(Boolean).join(" · ");
  if (sub) card.appendChild(el("div", "sub", sub));
  activate(card, () => {
    pendingTeamFacet = t.name;
    dirFilter.value = "";
    if (location.hash === "#/people") renderDirectory("people");
    else location.hash = "#/people";
  });
  return card;
}

function dirTopicRow(t) {
  const card = el("div", "card card-row dir-clickable");
  card.setAttribute("role", "button");
  card.tabIndex = 0;
  card.title = "Ask who knows about this";
  card.appendChild(el("span", "name", t.name));
  card.appendChild(el("span", "count", t.people + (t.people === 1 ? " person" : " people")));
  activate(card, () => runAsk(t.name));
  return card;
}

dirFilter.addEventListener("input", () => {
  if (currentView !== "ask") renderDirectory(currentView);
});
facetTeam.addEventListener("change", () => renderDirectory("people"));
facetOrg.addEventListener("change", () => renderDirectory("people"));

// Recall opens straight to your recent conversations, no typing required. The
// topic box and the source and time controls are optional refinements.
function openRecall() {
  runRecall();
}

function scheduleRecall() {
  if (recallTimer) clearTimeout(recallTimer);
  recallTimer = setTimeout(runRecall, 300);
}

if (recallQuery) recallQuery.addEventListener("input", scheduleRecall);
if (recallMe) recallMe.addEventListener("input", scheduleRecall);
if (recallSince) recallSince.addEventListener("change", applyRecallFilters);

// runRecall fetches the conversations you took part in. With no topic it
// returns your most recent ones; a topic narrows them. It then rebuilds the
// source filter and renders through applyRecallFilters.
async function runRecall() {
  const me = recallMe.value.trim();
  const query = recallQuery.value.trim();
  if (!me) {
    recallStatus.textContent = "Set who you are to see your conversations.";
    recallList.replaceChildren();
    if (recallSources) recallSources.replaceChildren();
    return;
  }
  recallStatus.textContent = "Looking\u2026";
  try {
    const res = await fetch(
      "/api/recall?me=" + encodeURIComponent(me) +
      (query ? "&q=" + encodeURIComponent(query) : "") + "&limit=25");
    const data = await res.json();
    if (!res.ok) {
      recallStatus.textContent = data.error || "Recall is unavailable.";
      recallList.replaceChildren();
      if (recallSources) recallSources.replaceChildren();
      return;
    }
    recallData = data;
    buildRecallSources(data);
    applyRecallFilters();
  } catch (err) {
    recallStatus.textContent = "Recall is unavailable.";
  }
}

// buildRecallSources renders a checkbox per source present in the results so
// you can narrow to the tools you care about. One source needs no filter.
function buildRecallSources(data) {
  if (!recallSources) return;
  const eps = data.episodes || [];
  const sources = [...new Set(eps.map((e) => e.source).filter(Boolean))].sort();
  recallSources.replaceChildren();
  recallSourceFilter = null;
  if (recallSlabel) recallSlabel.hidden = sources.length < 2;
  if (sources.length < 2) return;
  for (const src of sources) {
    const meta = sourceMeta(src);
    const chip = el("label", "recall-src");
    chip.style.setProperty("--sc", meta.color);
    const box = document.createElement("input");
    box.type = "checkbox";
    box.checked = true;
    box.value = src;
    box.addEventListener("change", onRecallSourceToggle);
    chip.appendChild(box);
    chip.appendChild(el("span", "rc-dot"));
    chip.appendChild(el("span", null, meta.label));
    recallSources.appendChild(chip);
  }
}

function onRecallSourceToggle() {
  const boxes = recallSources.querySelectorAll("input[type=checkbox]");
  const on = new Set();
  let allOn = true;
  for (const b of boxes) {
    if (b.checked) on.add(b.value);
    else allOn = false;
  }
  recallSourceFilter = allOn ? null : on;
  applyRecallFilters();
}

// applyRecallFilters renders the fetched conversations narrowed by the source
// checkboxes and the time dropdown, without re-querying the server.
function applyRecallFilters() {
  if (!recallData) return;
  let eps = recallData.episodes || [];
  const total = eps.length;
  const days = parseInt(recallSince ? recallSince.value : "0", 10) || 0;
  if (days > 0) {
    const cutoff = Date.now() - days * 86400000;
    eps = eps.filter((e) => !e.when || new Date(e.when).getTime() >= cutoff);
  }
  if (recallSourceFilter) {
    eps = eps.filter((e) => recallSourceFilter.has(e.source));
  }
  if (recallCount) {
    recallCount.textContent = !total ? ""
      : eps.length === total ? total + (total === 1 ? " conversation" : " conversations")
      : eps.length + " of " + total;
  }
  recallScope.textContent = (recallData.scope && recallData.scope.note) || "";
  recallList.replaceChildren();
  if (!eps.length) {
    recallStatus.textContent = total
      ? "No conversations match these filters."
      : "Nothing found in the conversations you took part in.";
    return;
  }
  recallStatus.textContent = "";
  for (const ep of eps) recallList.appendChild(recallCard(ep));
}

// recallCard builds the card for one remembered conversation.
// SOURCE_META gives each tool a distinct color and label, so a glance tells you
// where a conversation happened.
const SOURCE_META = {
  github: { label: "GitHub", color: "#a371f7" },
  slack: { label: "Slack", color: "#e01e5a" },
  pagerduty: { label: "PagerDuty", color: "#06ac38" },
  jira: { label: "Jira", color: "#2684ff" },
  confluence: { label: "Confluence", color: "#1d7afc" },
  gitlab: { label: "GitLab", color: "#fc6d26" },
  teams: { label: "Teams", color: "#6264a7" },
  linear: { label: "Linear", color: "#5e6ad2" },
  notion: { label: "Notion", color: "#b9b9b9" },
  zendesk: { label: "Zendesk", color: "#17a289" },
};

// sourceMeta returns the color and label for a source, or a neutral default.
function sourceMeta(src) {
  return SOURCE_META[(src || "").toLowerCase()] || { label: src || "source", color: "#8b93a3" };
}

// recallCard draws one conversation, colored and labeled by its source, with a
// clear place, who was there, and a button back to it.
function recallCard(ep) {
  const meta = sourceMeta(ep.source);
  const card = el("div", "card recall-card");
  card.style.setProperty("--sc", meta.color);

  const head = el("div", "rc-head");
  const src = el("span", "rc-source");
  src.appendChild(el("span", "rc-dot"));
  src.appendChild(el("span", null, meta.label));
  head.appendChild(src);
  if (ep.when) {
    head.appendChild(el("span", "rc-date", new Date(ep.when).toLocaleDateString(
      undefined, { year: "numeric", month: "short", day: "numeric" })));
  }
  card.appendChild(head);

  if (ep.place) {
    const place = ep.kind === "thread" || ep.kind === "window" ? "#" + ep.place : ep.place;
    card.appendChild(el("h3", "rc-title", place));
  }
  card.appendChild(el("p", "rc-people", recallPeople(ep)));

  if (ep.matched && ep.matched.length) chips(card, ep.matched);

  if (ep.solution) {
    const sol = el("div", "recall-solution");
    if (ep.solution.summary) sol.appendChild(el("p", "recall-summary", ep.solution.summary));
    for (const note of ep.solution.notes || []) {
      const line = el("p", "recall-note-line");
      line.appendChild(el("span", "recall-author", note.author + ": "));
      line.appendChild(document.createTextNode(note.text));
      sol.appendChild(line);
    }
    if (ep.solution.truncated) sol.appendChild(el("p", "recall-more", "The conversation ran longer."));
    card.appendChild(sol);
  }

  const foot = el("div", "rc-foot");
  if (ep.permalink) {
    const link = el("a", "rc-open");
    link.href = safeHref(ep.permalink);
    link.target = "_blank";
    link.rel = "noopener";
    link.appendChild(el("span", null, "Open in " + meta.label));
    link.appendChild(el("span", "rc-arrow", "\u2192"));
    foot.appendChild(link);
  }
  if (ep.link_may_have_expired) {
    foot.appendChild(el("span", "rc-stale", "link may be stale"));
  }
  if (foot.childNodes.length) card.appendChild(foot);

  return card;
}

// recallPeople names who else was in a conversation.
function recallPeople(ep) {
  const names = (ep.people || []).map((p) => p.name || p.email || p.id).filter(Boolean);
  if (!names.length) return "On your own";
  if (names.length === 1) return "With " + names[0];
  return "With " + names.slice(0, -1).join(", ") + " and " + names[names.length - 1];
}


// renderExposure draws where the organization is exposed: topics whose
// knowledge sits in too few people, and areas whose declared owner is not the
// one doing the work. Both come from /api/exposure, computed over the graph.
async function renderExposure() {
  expStatus.textContent = "Loading...";
  expRisk.replaceChildren();
  expDrift.replaceChildren();
  if (expRegions) expRegions.replaceChildren();
  let data;
  try {
    const res = await fetch("/api/exposure", { headers: { Accept: "application/json" } });
    if (!res.ok) throw new Error("HTTP " + res.status);
    data = await res.json();
  } catch (err) {
    expStatus.textContent = "Could not load exposure: " + err.message;
    return;
  }
  if (currentView !== "exposure") return;

  const risk = data.risk || [];
  const drift = data.drift || [];
  const regions = data.regions || [];
  const crit = risk.filter((r) => r.level === "critical").length;
  expStatus.textContent =
    risk.length + " topics scored, " + crit + " critical, " + drift.length + " drifting";

  if (expRegions) {
    if (regions.length) {
      for (const r of regions) expRegions.appendChild(regionCard(r));
    } else {
      expRegions.appendChild(el("p", "exp-empty",
        "No joined work found. Index git, Jira, Confluence, or GitHub: those record which subjects one piece of work touched."));
    }
  }
  if (expSpans) {
    const spans = data.spans || [];
    if (spans.length) {
      for (const sp of spans) expSpans.appendChild(spanCard(sp));
    } else {
      expSpans.appendChild(el("p", "exp-empty",
        "No one-person connections found. Index git, Jira, Confluence, or GitHub: those record which subjects one piece of work touched."));
    }
  }
  if (risk.length) {
    for (const r of risk) expRisk.appendChild(riskCard(r));
  } else {
    expRisk.appendChild(el("p", "exp-empty", "No topics scored yet. Index a source with expertise signal first."));
  }
  if (drift.length) {
    for (const d of drift) expDrift.appendChild(driftCard(d));
  } else {
    expDrift.appendChild(el("p", "exp-empty", "No ownership drift found, or no declared ownership indexed."));
  }
  fillDeparturePeople();
  // A departure check is shareable the same way a query is: /?dep=Gavin+Hudson
  // opens the exposure view with that person already checked.
  const params = new URLSearchParams(location.search);
  const dep = params.get("dep");
  if (dep && expDepResult && !expDepResult.childElementCount) checkDeparture(dep);
  if (params.get("proof") && expProofOut && !expProofOut.childElementCount) sealFinding();
}

// fillDeparturePeople stocks the person picker from the directory, once, so a
// name can be chosen rather than remembered.
async function fillDeparturePeople() {
  if (!expDepPeople || expDepPeople.childElementCount) return;
  try {
    const dir = await directory();
    for (const p of (dir.people || []).slice(0, 500)) {
      const opt = document.createElement("option");
      opt.value = p.name || p.id;
      expDepPeople.appendChild(opt);
    }
  } catch (err) {
    // The picker is a convenience; typing a name still works without it.
  }
}

// checkDeparture asks what one person's departure would cost and draws it.
async function checkDeparture(who) {
  const person = (who || "").trim();
  if (!person) return;
  if (expDepInput) expDepInput.value = person;
  expDepResult.replaceChildren();
  expDepResult.appendChild(el("p", "exp-empty", "Checking..."));
  let imp;
  try {
    const res = await fetch("/api/departure?person=" + encodeURIComponent(person),
      { headers: { Accept: "application/json" } });
    if (!res.ok) throw new Error("HTTP " + res.status);
    imp = await res.json();
  } catch (err) {
    expDepResult.replaceChildren();
    expDepResult.appendChild(el("p", "exp-empty", "Could not check that person: " + err.message));
    return;
  }
  expDepResult.replaceChildren();
  if (!imp.person) {
    expDepResult.appendChild(el("p", "exp-empty", "Nobody in the graph matches " + person + "."));
    return;
  }
  const sole = imp.sole || [];
  const top = imp.top || [];
  const card = el("div", "exp-card " + (sole.length ? "exp-critical" : top.length ? "exp-elevated" : "exp-ok"));
  const head = el("div", "exp-card-head");
  head.appendChild(el("span", "exp-topic", imp.name || imp.person));
  head.appendChild(el("span", "exp-level", sole.length ? "sole owner" : top.length ? "top expert" : "covered"));
  head.appendChild(el("span", "exp-bus", sole.length + " sole, " + top.length + " leading"));
  card.appendChild(head);
  if (sole.length) {
    card.appendChild(el("p", "exp-also", "Nobody else has any expertise in these:"));
    chips(card, sole);
  }
  if (top.length) {
    card.appendChild(el("p", "exp-also", "Strongest expert, but others remain:"));
    chips(card, top);
  }
  if (!sole.length && !top.length) {
    card.appendChild(el("p", "exp-also", "Leads nothing on their own. Their areas all have another expert."));
  }
  expDepResult.appendChild(card);
}

// regionCard draws one joined body of work and who it rests on. The subjects
// are listed in full rather than counted, because the point of the finding is
// how much one person would take with them.
function regionCard(r) {
  const topics = r.topics || [];
  const card = el("div", "exp-card exp-critical");
  const head = el("div", "exp-card-head");
  head.appendChild(el("span", "exp-topic", r.lead));
  head.appendChild(el("span", "exp-bus", topics.length + " joined subjects"));
  card.appendChild(head);
  card.appendChild(el("p", "exp-also", topics.join(", ")));
  return card;
}

// spanCard draws one connection that rests on a single person. It names how
// many people hold the two subjects between them, because that is what makes
// the finding surprising: the subjects are not short of experts, the crossing
// between them is.
function spanCard(sp) {
  const card = el("div", "exp-card exp-critical");
  const head = el("div", "exp-card-head");
  head.appendChild(el("span", "exp-topic", sp.topic + " + " + sp.with));
  head.appendChild(el("span", "exp-bus", "only " + sp.person));
  card.appendChild(head);
  card.appendChild(el("p", "exp-also",
    (sp.experts || 0) + " people hold the two subjects, and one has ever worked across them"));
  return card;
}

// riskCard draws one topic's knowledge concentration with its experts.
function riskCard(r) {
  const card = el("div", "exp-card exp-" + (r.level || "ok"));
  const head = el("div", "exp-card-head");
  head.appendChild(el("span", "exp-topic", r.topic));
  head.appendChild(el("span", "exp-level", r.level || "ok"));
  head.appendChild(el("span", "exp-bus", "bus factor " + r.busFactor));
  card.appendChild(head);
  if (r.includes && r.includes.length) {
    card.appendChild(el("p", "exp-also", "also called " + r.includes.join(", ")));
  }
  addRelated(card, r.topic);
  for (const e of r.experts || []) {
    const row = el("div", "exp-expert");
    row.appendChild(el("span", "exp-share", Math.round(e.share * 100) + "%"));
    const bar = el("span", "exp-bar");
    const fill = el("span", "exp-bar-fill");
    fill.style.width = Math.round(e.share * 100) + "%";
    bar.appendChild(fill);
    row.appendChild(bar);
    const name = el("button", "exp-name exp-name-btn", e.name);
    name.type = "button";
    name.title = "See what leaves with " + e.name;
    name.addEventListener("click", () => checkDeparture(e.id || e.name));
    row.appendChild(name);
    card.appendChild(row);
  }
  return card;
}

// driftCard draws one area where the declared owner is not the real expert.
function driftCard(d) {
  const card = el("div", "exp-card exp-drift-card");
  const head = el("div", "exp-card-head");
  head.appendChild(el("span", "exp-topic", d.topic));
  card.appendChild(head);
  const row = el("div", "exp-expert");
  row.appendChild(el("span", "exp-dim", "declared"));
  row.appendChild(el("span", "exp-declared", (d.declared || []).join(", ")));
  row.appendChild(el("span", "exp-dim", "actual"));
  row.appendChild(el("span", "exp-actual", d.actual));
  card.appendChild(row);
  return card;
}


// addRelated hangs the topics that share this one's experts under its card, so a
// subject and its specialties read as one body of knowledge rather than as
// unrelated rows that happen to sit near each other.
async function addRelated(card, topic) {
  try {
    const res = await fetch("/api/related?topic=" + encodeURIComponent(topic) + "&limit=4",
      { headers: { Accept: "application/json" } });
    if (!res.ok) return;
    const data = await res.json();
    const rel = (data.related || []).filter((r) => r.topic !== topic);
    if (!rel.length) return;
    const line = el("p", "exp-also");
    line.appendChild(el("span", "exp-dim", "shares experts with "));
    rel.forEach((r, i) => {
      if (i) line.appendChild(el("span", "exp-dim", ", "));
      const t = el("span", "exp-rel", r.topic);
      t.title = Math.round(r.overlap * 100) + "% of its experts also hold " + topic +
        (r.narrower ? ", and it is the narrower of the two" : "");
      line.appendChild(t);
    });
    card.appendChild(line);
  } catch (err) {
    // Related topics are extra context; a card without them is still complete.
  }
}

// sealFinding asks the server to sign the current finding and shows what came
// back, plus the two commands that check it without trusting this page.
async function sealFinding() {
  expProofOut.replaceChildren();
  expProofOut.appendChild(el("p", "exp-empty", "Signing..."));
  let bundle;
  try {
    const res = await fetch("/api/attest", { headers: { Accept: "application/json" } });
    if (!res.ok) throw new Error("HTTP " + res.status);
    bundle = await res.json();
  } catch (err) {
    expProofOut.replaceChildren();
    expProofOut.appendChild(el("p", "exp-empty", "Could not seal the finding: " + err.message));
    return;
  }
  const claim = (bundle.claims || [])[0] || {};
  const ev = (claim.evidence || [])[0] || {};
  const producer = bundle.producer || {};
  const sigs = bundle.signatures || [];

  const card = el("div", "exp-card exp-ok");
  const head = el("div", "exp-card-head");
  head.appendChild(el("span", "exp-topic", claim.type || "whodar.knowledge-risk/1"));
  head.appendChild(el("span", "exp-level", sigs.length ? "signed" : "unsigned"));
  head.appendChild(el("span", "exp-bus", bundle.bundle_id || ""));
  card.appendChild(head);

  const rows = [
    ["signing key", producer.key_id || ""],
    ["evidence digest", ev.digest || ""],
    ["claimed at", claim.at || ""],
    ["chain", (bundle.chain || {}).profile || ""],
  ];
  for (const [k, v] of rows) {
    if (!v) continue;
    const row = el("div", "exp-expert");
    row.appendChild(el("span", "exp-dim", k));
    row.appendChild(el("span", "exp-mono", v));
    card.appendChild(row);
  }
  expProofOut.replaceChildren(card);

  const dl = el("a", "exp-dl", "Download the bundle");
  dl.href = "/api/attest?download=1";
  dl.setAttribute("download", "whodar-knowledge-risk.loomseal.json");
  expProofOut.appendChild(dl);
  const how = el("p", "exp-also",
    "Verify it anywhere, offline: loomseal verify whodar-knowledge-risk.loomseal.json");
  expProofOut.appendChild(how);
}


// recordedRuns loads the transcripts of commands this demo cannot run itself,
// fetched once. A run that needs a model is recorded rather than faked, so what
// is on screen is what the command actually printed somewhere.
let recordedCache = null;
async function recordedRuns() {
  if (recordedCache) return recordedCache;
  const res = await fetch("/static/recorded.json", { headers: { Accept: "application/json" } });
  if (!res.ok) throw new Error("HTTP " + res.status);
  recordedCache = await res.json();
  return recordedCache;
}

// renderCLI prints one command's real terminal output. The tabs are built once
// and the selected one is fetched fresh, so what appears is what the command
// prints right now against this index.
async function renderCLI(cmd) {
  cliCurrent = cmd;
  if (!cliTabs.childElementCount) {
    for (const c of CLI_COMMANDS) {
      const b = el("button", "cli-tab" + (c.recorded ? " cli-tab-rec" : ""), c.label);
      b.type = "button";
      if (c.recorded) b.title = "Recorded: this run needed a model, which the demo does not run";
      b.dataset.cmd = c.cmd;
      b.addEventListener("click", () => renderCLI(c.cmd));
      cliTabs.appendChild(b);
    }
  }
  for (const b of cliTabs.children) {
    b.classList.toggle("active", b.dataset.cmd === cmd);
  }
  cliStatus.textContent = "Running...";
  cliOut.textContent = "";
  cliNote.textContent = "";
  cliNote.hidden = true;
  const spec = CLI_COMMANDS.find((c) => c.cmd === cmd) || {};
  if (spec.recorded) {
    try {
      const rec = await recordedRuns();
      const run = rec[cmd];
      if (!run) throw new Error("not recorded");
      if (cliCurrent !== cmd) return;
      cliOut.textContent = run.text;
      cliNote.textContent = run.note;
      cliNote.hidden = false;
      cliStatus.textContent = "recorded, not live";
    } catch (err) {
      cliStatus.textContent = "Could not load that: " + err.message;
    }
    return;
  }
  try {
    const res = await fetch("/api/cli?cmd=" + encodeURIComponent(cmd), {
      headers: { Accept: "text/plain" },
    });
    if (!res.ok) throw new Error("HTTP " + res.status);
    const text = await res.text();
    if (cliCurrent !== cmd) return;
    const label = (CLI_COMMANDS.find((c) => c.cmd === cmd) || {}).label || cmd;
    cliOut.textContent = "$ " + label + "\n\n" + text;
    cliStatus.textContent = "";
  } catch (err) {
    cliStatus.textContent = "Could not run that: " + err.message;
  }
}

if (expProofGo) {
  expProofGo.addEventListener("click", sealFinding);
}
if (expDepGo) {
  expDepGo.addEventListener("click", () => checkDeparture(expDepInput.value));
}
if (expDepInput) {
  expDepInput.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      checkDeparture(expDepInput.value);
    }
  });
}

window.addEventListener("hashchange", () => showView(viewFromHash()));

// A shared link carries the question and person in the URL; run them on load.
const linked = new URLSearchParams(location.search);
if (linked.get("q")) {
  qInput.value = linked.get("q");
  ask();
}
if (linked.get("person")) {
  openProfile(linked.get("person"));
}
showView(viewFromHash());
// Examples come from the index, so they run once the directory helpers below
// exist: called any earlier they hit the uninitialized cache and fall back.
showExamples();
fillNavCounts();
loadModes();
