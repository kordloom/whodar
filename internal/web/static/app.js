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

const EXAMPLE_QUERIES = [
  "who do I talk to about billing retries",
  "who knows terraform",
  "kubernetes deploys",
];

for (const example of EXAMPLE_QUERIES) {
  const b = el("button", "example", example);
  b.type = "button";
  b.addEventListener("click", () => runAsk(example));
  examplesEl.appendChild(b);
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
  return DIR_VIEWS[name] ? name : "ask";
}

// showView flips between the ask view and a directory view and marks the nav.
function showView(view) {
  currentView = view;
  askView.hidden = view !== "ask";
  dirView.hidden = view === "ask" || view === "recall";
  if (recallView) recallView.hidden = view !== "recall";
  for (const a of sideNav.querySelectorAll("a")) {
    a.classList.toggle("active", a.dataset.view === view);
  }
  facetTeam.hidden = view !== "people";
  facetOrg.hidden = view !== "people";
  if (view === "recall") {
    recallQuery.focus();
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

function dirTeamRow(t) {
  const card = el("div", "card");
  const name = el("div", "name");
  const toggle = el("button", "name-toggle", t.name);
  toggle.type = "button";
  toggle.title = "Show this team's people";
  toggle.addEventListener("click", () => {
    pendingTeamFacet = t.name;
    dirFilter.value = "";
    if (location.hash === "#/people") {
      renderDirectory("people");
    } else {
      location.hash = "#/people";
    }
  });
  name.appendChild(toggle);
  card.appendChild(name);
  const sub = [t.org, t.people + (t.people === 1 ? " person" : " people")].filter(Boolean).join(" · ");
  if (sub) card.appendChild(el("div", "sub", sub));
  return card;
}

function dirTopicRow(t) {
  const card = el("div", "card card-row");
  const toggle = el("button", "name-toggle", t.name);
  toggle.type = "button";
  toggle.title = "Ask who knows about this";
  toggle.addEventListener("click", () => runAsk(t.name));
  card.appendChild(toggle);
  card.appendChild(el("span", "count", t.people + (t.people === 1 ? " person" : " people")));
  return card;
}

dirFilter.addEventListener("input", () => {
  if (currentView !== "ask") renderDirectory(currentView);
});
facetTeam.addEventListener("change", () => renderDirectory("people"));
facetOrg.addEventListener("change", () => renderDirectory("people"));

if (recallForm) {
  recallForm.addEventListener("submit", (e) => {
    e.preventDefault();
    runRecall();
  });
}

// runRecall asks what the named person worked through before and renders the
// conversations, each with who was there and a link back to it.
async function runRecall() {
  const query = recallQuery.value.trim();
  const me = recallMe.value.trim();
  if (!query) return;
  if (!me) {
    recallStatus.textContent = "Say who you are, so the answer covers your own conversations.";
    return;
  }
  recallStatus.textContent = "Looking...";
  recallList.replaceChildren();
  recallScope.textContent = "";
  try {
    const res = await fetch(
      "/api/recall?q=" + encodeURIComponent(query) + "&me=" + encodeURIComponent(me));
    const data = await res.json();
    if (!res.ok) {
      recallStatus.textContent = data.error || "Recall is unavailable.";
      return;
    }
    renderRecall(data);
  } catch (err) {
    recallStatus.textContent = "Recall is unavailable.";
  }
}

// renderRecall draws one card per remembered conversation.
function renderRecall(data) {
  const episodes = data.episodes || [];
  recallScope.textContent = (data.scope && data.scope.note) || "";
  if (!episodes.length) {
    recallStatus.textContent = "Nothing found in the conversations you took part in.";
    return;
  }
  recallStatus.textContent = "";
  for (const ep of episodes) {
    recallList.appendChild(recallCard(ep));
  }
}

// recallCard builds the card for one remembered conversation.
function recallCard(ep) {
  const card = el("div", "card");
  const head = el("div", "card-head");
  head.appendChild(el("h3", null, recallPeople(ep)));
  const badge = confidenceBadge(ep.confidence);
  if (badge) head.appendChild(badge);
  card.appendChild(head);

  const where = [];
  if (ep.place) where.push(ep.kind === "thread" || ep.kind === "window" ? "#" + ep.place : ep.place);
  if (ep.when) where.push(new Date(ep.when).toLocaleDateString());
  if (ep.source) where.push(ep.source);
  card.appendChild(el("p", "card-sub", where.join(" · ")));

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

  if (ep.permalink) {
    const link = el("a", "recall-link", "Open the conversation");
    link.href = ep.permalink;
    link.target = "_blank";
    link.rel = "noopener";
    card.appendChild(link);
  }
  if (ep.link_may_have_expired) {
    card.appendChild(el("p", "recall-more", "Old enough that the link may no longer resolve."));
  }
  return card;
}

// recallPeople names who else was in a conversation.
function recallPeople(ep) {
  const names = (ep.people || []).map((p) => p.name || p.email || p.id).filter(Boolean);
  if (!names.length) return "On your own";
  if (names.length === 1) return "With " + names[0];
  return "With " + names.slice(0, -1).join(", ") + " and " + names[names.length - 1];
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
fillNavCounts();
loadModes();
