// whodar org chart: an interactive, filterable, navigable view of the reporting
// graph. Reads /api/directory (people with managerId, team, topics). No external
// libraries: the page CSP is default-src 'self'.
(function () {
	"use strict";

	var NODE_W = 216, X_STEP = 244, LEVEL_H = 132;
	var SVGNS = "http://www.w3.org/2000/svg";

	var el = {
		stage: document.getElementById("oc-stage"),
		canvas: document.getElementById("oc-canvas"),
		links: document.getElementById("oc-links"),
		nodes: document.getElementById("oc-nodes"),
		empty: document.getElementById("oc-empty"),
		detail: document.getElementById("oc-detail"),
		search: document.getElementById("oc-search"),
		team: document.getElementById("oc-team"),
		topic: document.getElementById("oc-topic"),
		zoomLabel: document.getElementById("oc-zoomlevel"),
		crumbs: document.getElementById("oc-crumbs"),
		contact: document.getElementById("oc-contact"),
	};

	var nodes = new Map();
	var roots = [];
	var collapsed = new Set();
	var domNodes = new Map();
	var positions = new Map();
	var rootSet = new Set();
	var selected = null;
	var searchMatches = [], searchIdx = -1;
	var selection = new Set();
	var view = { tx: 40, ty: 30, scale: 1 };
	// How much a pinch zooms per wheel event, and the largest single delta that
	// counts. Trackpads report wildly different magnitudes, so both are needed to
	// keep the gesture steady across machines.
	var ZOOM_SENSITIVITY = 0.0015;
	var ZOOM_STEP_CAP = 40;

	// Restrained, cool gradient pairs so avatars add life on-brand. Keyed by team.
	var avatarPalette = [
		["#7aa0e0", "#4c6ef5"], ["#9d8bd8", "#7048e8"], ["#5ec2c9", "#2c9faf"],
		["#e295b4", "#c2557f"], ["#8098d0", "#4a5fa5"], ["#cda874", "#b07d3a"],
		["#93a6c4", "#5f7599"], ["#6ab6dc", "#3a86b0"]
	];

	function api(path) {
		return fetch(path, { headers: { Accept: "application/json" } }).then(function (r) {
			if (!r.ok) throw new Error(path + ": " + r.status);
			return r.json();
		});
	}
	function esc(s) {
		return (s == null ? "" : String(s)).replace(/[&<>"']/g, function (ch) {
			return ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[ch];
		});
	}
	function initials(name) {
		var parts = (name || "?").trim().split(/\s+/);
		return ((parts[0] ? parts[0][0] : "?") + (parts.length > 1 ? parts[parts.length - 1][0] : "")).toUpperCase();
	}
	function avatarColor(n) {
		var key = n.team || n.name || n.id, h = 0;
		for (var i = 0; i < key.length; i++) h = (h * 31 + key.charCodeAt(i)) >>> 0;
		var p = avatarPalette[h % avatarPalette.length];
		return "linear-gradient(135deg, " + p[0] + ", " + p[1] + ")";
	}

	// --- graph ---
	var _depthCache = new Map(), _parent = null;
	function buildGraph(dir) {
		nodes.clear(); roots = []; collapsed.clear();
		_parent = null; _depthCache = new Map();
		var people = dir.people || [], byId = new Map();
		people.forEach(function (p) {
			byId.set(p.id, p);
			nodes.set(p.id, {
				id: p.id, kind: "person", name: p.name || p.id, title: p.title || "",
				team: p.team || "", email: p.email || "", topics: p.topics || [], childIds: [],
			});
		});
		var edges = 0;
		people.forEach(function (p) { if (p.managerId && byId.has(p.managerId) && p.managerId !== p.id) edges++; });
		if (edges > 0) {
			people.forEach(function (p) {
				if (p.managerId && nodes.has(p.managerId) && p.managerId !== p.id) nodes.get(p.managerId).childIds.push(p.id);
				else roots.push(p.id);
			});
		} else {
			var seen = {};
			people.forEach(function (p) {
				if (!p.team) { roots.push(p.id); return; }
				var tid = "team::" + p.team;
				if (!seen[tid]) { seen[tid] = 1; nodes.set(tid, { id: tid, kind: "team", name: p.team, title: "", team: "", topics: [], childIds: [] }); roots.push(tid); }
				nodes.get(tid).childIds.push(p.id);
			});
		}
		// Break any manager cycles (cross-source merges can make two people each
		// other's manager) so the depth, layout, and ancestor walks terminate.
		nodes.forEach(function (n, id) {
			var seen = {};
			for (var p = parentOf(id); p != null; p = parentOf(p)) {
				if (seen[p]) {
					var par = parentOf(p), kids = nodes.get(par).childIds, i = kids.indexOf(p);
					if (i >= 0) kids.splice(i, 1);
					if (roots.indexOf(p) < 0) roots.push(p);
					_parent = null;
					break;
				}
				seen[p] = 1;
			}
		});
		_parent = null;
		var byName = function (a, b) { return nodes.get(a).name.localeCompare(nodes.get(b).name); };
		nodes.forEach(function (n) { n.childIds.sort(byName); });
		roots.sort(byName);
		rootSet = new Set(roots);
		if (nodes.size > 55) nodes.forEach(function (n, id) { if (depthOf(id) >= 1 && n.childIds.length) collapsed.add(id); });
	}
	function parentOf(id) {
		if (_parent == null) { _parent = new Map(); nodes.forEach(function (n) { n.childIds.forEach(function (c) { _parent.set(c, n.id); }); }); }
		return _parent.has(id) ? _parent.get(id) : null;
	}
	function depthOf(id) {
		if (_depthCache.has(id)) return _depthCache.get(id);
		var p = parentOf(id), d = p == null ? 0 : depthOf(p) + 1;
		_depthCache.set(id, d); return d;
	}
	function visibleChildren(id) { return collapsed.has(id) ? [] : nodes.get(id).childIds; }

	function layout() {
		positions.clear();
		var cursor = 0;
		function walk(id, depth) {
			var kids = visibleChildren(id), y = depth * LEVEL_H;
			if (!kids.length) { var x = cursor * X_STEP; cursor++; positions.set(id, { x: x, y: y }); return x; }
			var xs = kids.map(function (k) { return walk(k, depth + 1); });
			var cx = (xs[0] + xs[xs.length - 1]) / 2;
			positions.set(id, { x: cx, y: y }); return cx;
		}
		roots.forEach(function (r) { walk(r, 0); });
	}

	function nodeEl(n) {
		var d = domNodes.get(n.id);
		if (d) return d;
		d = document.createElement("div");
		d.className = "oc-node" + (n.kind === "team" ? " oc-team" : "") + (rootSet.has(n.id) ? " oc-root" : "");
		d.dataset.id = n.id;
		var body = '<div class="oc-txt"><div class="nm"></div>';
		if (n.kind === "person") body += '<div class="ti"></div>';
		if (n.team) body += '<div class="tm"></div>';
		body += '</div>';
		var tog = n.childIds.length ? '<button class="oc-toggle" type="button" aria-label="Toggle reports" title="Collapse or expand reports"><svg class="oc-chev" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 6l4 4 4-4"></path></svg><span class="oc-toggle-n"></span></button>' : '';
		d.innerHTML = '<div class="oc-av"></div>' + body + tog;
		var av = d.querySelector(".oc-av");
		av.textContent = initials(n.name);
		av.style.background = avatarColor(n);
		d.querySelector(".nm").textContent = n.name;
		if (n.kind === "person") d.querySelector(".ti").textContent = n.title || " ";
		if (n.team) d.querySelector(".tm").textContent = n.team;
		el.nodes.appendChild(d);
		domNodes.set(n.id, d);
		return d;
	}

	function render() {
		layout();
		domNodes.forEach(function (d) { d.style.display = "none"; });
		var maxX = -Infinity, maxY = 0;
		positions.forEach(function (pos, id) {
			var n = nodes.get(id), d = nodeEl(n);
			d.style.display = "";
			d.style.transform = "translate(" + pos.x + "px," + pos.y + "px)";
			d.classList.toggle("picked", selection.has(id));
			d.classList.toggle("is-collapsed", collapsed.has(id));
			var tn = d.querySelector(".oc-toggle-n"); if (tn) tn.textContent = collapsed.has(id) ? String(subtreeCount(id)) : "";
			maxX = Math.max(maxX, pos.x + NODE_W);
			maxY = Math.max(maxY, pos.y + d.offsetHeight);
		});
		drawLinks();
		el.canvas.style.width = (isFinite(maxX) ? maxX + 40 : 0) + "px";
		el.canvas.style.height = (maxY + 40) + "px";
		el.links.setAttribute("width", (isFinite(maxX) ? maxX + 40 : 0));
		el.links.setAttribute("height", maxY + 40);
		applyFilters();
		applyView();
	}
	function subtreeCount(id) { var n = 0; (nodes.get(id).childIds || []).forEach(function (c) { n += 1 + subtreeCount(c); }); return n; }

	// elbowPath draws a clean rounded orthogonal connector: down, across, down.
	function elbowPath(px, py, cx, cy) {
		if (Math.abs(cx - px) < 1) return "M" + px + " " + py + " L" + cx + " " + cy;
		var midY = (py + cy) / 2;
		var r = Math.min(14, Math.abs(cx - px) / 2, Math.abs(midY - py), Math.abs(cy - midY));
		var sgn = cx > px ? 1 : -1;
		return "M" + px + " " + py +
			" L" + px + " " + (midY - r) +
			" Q" + px + " " + midY + " " + (px + sgn * r) + " " + midY +
			" L" + (cx - sgn * r) + " " + midY +
			" Q" + cx + " " + midY + " " + cx + " " + (midY + r) +
			" L" + cx + " " + cy;
	}
	function drawLinks() {
		while (el.links.firstChild) el.links.removeChild(el.links.firstChild);
		positions.forEach(function (pos, id) {
			var d = domNodes.get(id), px = pos.x + NODE_W / 2, py = pos.y + d.offsetHeight;
			visibleChildren(id).forEach(function (cid) {
				var c = positions.get(cid), cx = c.x + NODE_W / 2, cy = c.y;
				var path = document.createElementNS(SVGNS, "path");
				path.setAttribute("class", "oc-link");
				path.setAttribute("d", elbowPath(px, py, cx, cy));
				el.links.appendChild(path);
			});
		});
	}

	// --- viewport ---
	function applyView() {
		el.canvas.style.transform = "translate(" + view.tx + "px," + view.ty + "px) scale(" + view.scale + ")";
		if (el.zoomLabel) el.zoomLabel.textContent = Math.round(view.scale * 100) + "%";
	}
	function fit() {
		var w = el.canvas.offsetWidth, h = el.canvas.offsetHeight;
		if (!w || !h) { view.scale = 1; view.tx = 40; view.ty = 30; applyView(); return; }
		var sw = Math.max(100, el.stage.clientWidth - 80), sh = Math.max(100, el.stage.clientHeight - 80);
		var s = Math.min(1, Math.min(sw / w, sh / h));
		view.scale = (isFinite(s) && s > 0) ? s : 1;
		view.tx = Math.max(24, (el.stage.clientWidth - w * view.scale) / 2);
		view.ty = 30;
		applyView();
	}
	function zoomAround(cx, cy, factor) {
		var ns = Math.max(0.15, Math.min(2.6, view.scale * factor));
		view.tx = cx - (cx - view.tx) * (ns / view.scale);
		view.ty = cy - (cy - view.ty) * (ns / view.scale);
		view.scale = ns;
		applyView();
	}
	function zoomButton(factor) { var r = el.stage.getBoundingClientRect(); zoomAround(r.width / 2, r.height / 2, factor); }

	// --- filters ---
	// nodeMatches reports whether a node matches the free-text query. People match
	// on name, email, title, team, and topics so a search finds a person by any of
	// them, not just their name; team nodes match on their name.
	function nodeMatches(n, q) {
		if (!q) return true;
		var t = n.name;
		if (n.kind === "person") t += " " + (n.email || "") + " " + (n.title || "") + " " + (n.team || "") + " " + (n.topics || []).join(" ");
		return t.toLowerCase().indexOf(q) >= 0;
	}
	function applyFilters() {
		var q = el.search.value.trim().toLowerCase(), team = el.team.value, topic = el.topic.value;
		var any = q || team || topic;
		if (q) nodes.forEach(function (n, id) {
			if (n.kind === "person" && nodeMatches(n, q))
				for (var p = parentOf(id); p != null; p = parentOf(p)) collapsed.delete(p);
		});
		var passes = function (n) {
			return nodeMatches(n, q) && (!team || n.team === team) && (!topic || (n.topics || []).indexOf(topic) >= 0);
		};
		domNodes.forEach(function (d, id) {
			var n = nodes.get(id), ok = passes(n);
			d.classList.toggle("dim", any && !ok);
			d.classList.toggle("match", !!q && ok && n.kind === "person");
		});
		// Count and step over every matching person, including any still inside a
		// collapsed subtree, so a pasted query is never miscounted as no matches.
		var matches = [];
		if (q) nodes.forEach(function (n, id) { if (n.kind === "person" && passes(n)) matches.push(id); });
		matches.sort(function (a, b) { return nodes.get(a).name.localeCompare(nodes.get(b).name); });
		searchMatches = matches;
		if (searchIdx >= matches.length) searchIdx = -1;
		markCurrent();
		updateSearchCount(q);
	}
	// updateSearchCount shows how many people match, and the position while
	// stepping through them, in the badge beside the search box.
	function updateSearchCount(q) {
		var c = document.getElementById("oc-searchcount");
		if (!c) return;
		if (!q) { c.textContent = ""; return; }
		if (!searchMatches.length) { c.textContent = "no matches"; return; }
		c.textContent = (searchIdx >= 0 ? (searchIdx + 1) + "/" : "") + searchMatches.length + " match" + (searchMatches.length === 1 ? "" : "es");
	}
	// markCurrent rings the person the search is centered on while stepping.
	function markCurrent() {
		domNodes.forEach(function (d) { d.classList.remove("current"); });
		if (searchIdx >= 0 && searchIdx < searchMatches.length) {
			var d = domNodes.get(searchMatches[searchIdx]);
			if (d) d.classList.add("current");
		}
	}
	// stepSearch advances to the next match, cycling, and centers on it.
	function stepSearch() {
		if (!searchMatches.length) return false;
		searchIdx = (searchIdx + 1) % searchMatches.length;
		focusNode(searchMatches[searchIdx]);
		return true;
	}
	function populateFilters(dir) {
		(dir.teams || []).forEach(function (t) { addOption(el.team, t.name, t.name + " · " + t.people); });
		(dir.topics || []).slice(0, 200).forEach(function (t) { addOption(el.topic, t.name, t.name + " · " + t.people); });
	}
	function addOption(sel, val, label) { var o = document.createElement("option"); o.value = val; o.textContent = label; sel.appendChild(o); }

	// --- detail panel ---
	function paintAvatars(container) {
		container.querySelectorAll("[data-av]").forEach(function (a) {
			var m = nodes.get(a.getAttribute("data-av"));
			if (m) { a.textContent = initials(m.name); a.style.background = avatarColor(m); }
		});
	}
	function personRow(pid) {
		var m = nodes.get(pid);
		if (!m) return "";
		return '<button class="d-person" data-goto="' + esc(pid) + '">' +
			'<span class="oc-av d-av-sm" data-av="' + esc(pid) + '"></span>' +
			'<span class="d-person-txt"><span class="d-person-nm">' + esc(m.name) + '</span>' +
			'<span class="d-person-ti">' + esc(m.title || m.team || "") + '</span></span>' +
			'<span class="d-person-go">›</span></button>';
	}
	function updateCrumbs(id) {
		if (!el.crumbs) return;
		if (!id || !nodes.has(id)) { el.crumbs.innerHTML = ""; el.crumbs.classList.remove("on"); return; }
		var path = [];
		for (var c = id; c != null; c = parentOf(c)) path.unshift(c);
		el.crumbs.classList.add("on");
		el.crumbs.innerHTML = path.map(function (pid, i) {
			var sep = i > 0 ? '<span class="oc-crumb-sep">\u203a</span>' : "";
			var cur = i === path.length - 1 ? " oc-crumb-cur" : "";
			return sep + '<button class="oc-crumb' + cur + '" data-goto="' + esc(pid) + '">' + esc(nodes.get(pid).name) + '</button>';
		}).join("");
		el.crumbs.querySelectorAll(".oc-crumb").forEach(function (b) { b.onclick = function () { focusNode(b.getAttribute("data-goto")); }; });
	}
	function openDetail(id) {
		var n = nodes.get(id);
		if (!n || n.kind !== "person") return;
		select(id);
		el.detail.hidden = false;
		renderDetail(id);
		updateCrumbs(id);
	}
	function renderDetail(id) {
		var n = nodes.get(id), mgr = parentOf(id), reports = n.childIds || [], topics = n.topics || [];
		var h = '<button class="d-close" aria-label="Close">✕</button>';
		h += '<div class="d-hero"><div class="oc-av d-av" data-av="' + esc(id) + '"></div>' +
			'<div class="d-hero-txt"><h2>' + esc(n.name) + '</h2><p class="d-ti">' + esc(n.title || "") + '</p>' +
			(n.team ? '<span class="d-team">' + esc(n.team) + '</span>' : '') + '</div></div>';
		var menu = '<div class="d-menu"><button class="d-btn" data-act="contact" title="Email, calendar invite, or Slack handle">Contact ▾</button>' +
			'<div class="d-menu-list" hidden>' +
			(n.email ? '<button data-m="email">Email</button>' : '') +
			(n.email ? '<button data-m="cal">Calendar invite</button>' : '') +
			(n.email ? '<button data-m="copy">Copy email</button>' : '') +
			'<button data-m="slack">Copy Slack handle</button></div></div>';
		h += '<div class="d-actions">' +
			'<button class="d-btn d-btn-primary" data-act="focus" title="Center the org chart on this person">Focus</button>' +
			(reports.length ? '<button class="d-btn" data-act="expand" title="Expand this person&#39;s direct reports in the chart">Reports</button>' : '') +
			menu + '</div>';
		h += '<div class="d-sec"><div class="d-lab">Reports to</div>' +
			(mgr ? '<div class="d-people">' + personRow(mgr) + '</div>' : '<div class="d-empty-line">Top of the organization</div>') + '</div>';
		h += '<div class="d-sec"><div class="d-lab">Direct reports · ' + reports.length + '</div>' +
			(reports.length ? '<div class="d-people">' + reports.map(personRow).join("") + '</div>' : '<div class="d-empty-line">None</div>') + '</div>';
		if (topics.length) h += '<div class="d-sec"><div class="d-lab">Works on</div><div class="d-chips">' +
			topics.map(function (t) { return '<span class="d-chip">' + esc(t) + '</span>'; }).join("") + '</div></div>';
		el.detail.innerHTML = h;
		paintAvatars(el.detail);
		el.detail.querySelector(".d-close").onclick = closeDetail;
		var fb = el.detail.querySelector('[data-act="focus"]'); if (fb) fb.onclick = function () { focusNode(id); };
		var eb = el.detail.querySelector('[data-act="expand"]'); if (eb) eb.onclick = function () { collapsed.delete(id); render(); };
		var cb = el.detail.querySelector('[data-act="contact"]');
		if (cb) cb.onclick = function (e) { e.stopPropagation(); var m = el.detail.querySelector('.d-menu-list'); if (m) m.hidden = !m.hidden; };
		el.detail.querySelectorAll('.d-menu-list [data-m]').forEach(function (b) {
			b.onclick = function () {
				var m = b.getAttribute('data-m');
				if (m === 'email' && n.email) window.location.href = 'mailto:' + n.email;
				else if (m === 'cal' && n.email) downloadICS([n]);
				else if (m === 'copy' && n.email) copyText(n.email, 'Copied ' + n.email);
				else if (m === 'slack') copyText(handleFor(n), 'Copied ' + handleFor(n));
				var list = el.detail.querySelector('.d-menu-list'); if (list) list.hidden = true;
			};
		});
		el.detail.querySelectorAll(".d-person").forEach(function (b) { b.onclick = function () { focusNode(b.getAttribute("data-goto")); }; });
	}
	function closeDetail() { el.detail.hidden = true; select(null); updateCrumbs(null); }
	function select(id) {
		if (selected && domNodes.has(selected)) domNodes.get(selected).classList.remove("sel");
		selected = id;
		if (id && domNodes.has(id)) domNodes.get(id).classList.add("sel");
	}
	function focusNode(id) {
		if (!nodes.has(id)) return;
		for (var p = parentOf(id); p != null; p = parentOf(p)) collapsed.delete(p);
		render();
		var pos = positions.get(id);
		if (pos) {
			view.scale = Math.max(view.scale, 0.85);
			view.tx = el.stage.clientWidth / 2 - (pos.x + NODE_W / 2) * view.scale;
			view.ty = el.stage.clientHeight / 3 - pos.y * view.scale;
			applyView();
		}
		openDetail(id);
	}

	// --- events ---
	function byId(id, fn) { var b = document.getElementById(id); if (b) fn(b); }
	function jumpToMe(me) {
		var meL = me.toLowerCase(), hit = null;
		nodes.forEach(function (n, id) {
			if (hit || n.kind !== "person") return;
			if ((n.email || "").toLowerCase() === meL || n.name.toLowerCase() === meL) hit = id;
		});
		if (hit) focusNode(hit); else toast("You (" + me + ") are not in this org.");
	}
	function selectedPeople() {
		var out = [];
		selection.forEach(function (id) { var n = nodes.get(id); if (n && n.kind === "person") out.push(n); });
		return out;
	}
	function handleFor(p) {
		var base = p.email ? p.email.split("@")[0] : (p.name || "").toLowerCase().replace(/[^a-z0-9]+/g, ".");
		return "@" + base.replace(/^\.+|\.+$/g, "");
	}
	function pad2(n) { return (n < 10 ? "0" : "") + n; }
	function icsStamp(d) {
		return d.getUTCFullYear() + pad2(d.getUTCMonth() + 1) + pad2(d.getUTCDate()) + "T" + pad2(d.getUTCHours()) + pad2(d.getUTCMinutes()) + "00Z";
	}
	function downloadICS(people) {
		var start = new Date(Date.now() + 3600000); start.setMinutes(0, 0, 0);
		var end = new Date(start.getTime() + 1800000);
		var lines = ["BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//whodar//org chart//EN", "CALSCALE:GREGORIAN", "METHOD:PUBLISH", "BEGIN:VEVENT",
			"UID:whodar-" + icsStamp(start) + "@whodar", "DTSTAMP:" + icsStamp(new Date()), "DTSTART:" + icsStamp(start), "DTEND:" + icsStamp(end),
			"SUMMARY:whodar: " + people.map(function (p) { return p.name; }).join(", ")];
		people.forEach(function (p) { if (p.email) lines.push("ATTENDEE;CN=" + p.name + ";RSVP=TRUE:mailto:" + p.email); });
		lines.push("END:VEVENT", "END:VCALENDAR");
		var blob = new Blob([lines.join(String.fromCharCode(13, 10))], { type: "text/calendar;charset=utf-8" });
		var url = URL.createObjectURL(blob);
		var a = document.createElement("a"); a.href = url; a.download = "whodar-event.ics";
		document.body.appendChild(a); a.click(); a.remove(); URL.revokeObjectURL(url);
	}
	function updateContactBar() {
		if (!el.contact) return;
		var people = selectedPeople();
		if (!people.length) { el.contact.hidden = true; el.contact.innerHTML = ""; return; }
		var emails = people.map(function (p) { return p.email; }).filter(Boolean);
		var dis = emails.length ? "" : " disabled";
		el.contact.hidden = false;
		el.contact.innerHTML =
			'<div class="oc-contact-row">' +
			'<span class="oc-contact-n">' + people.length + ' selected</span>' +
			'<button class="oc-cbtn" data-c="email"' + dis + '>Email</button>' +
			'<button class="oc-cbtn" data-c="cal"' + dis + '>Calendar</button>' +
			'<button class="oc-cbtn" data-c="slack">Slack</button>' +
			'<button class="oc-cbtn oc-cbtn-x" data-c="clear">Clear</button>' +
			'</div>' +
			'<div class="oc-copybox" hidden><input class="oc-copyin" type="text" readonly aria-label="Slack handles"><button class="oc-cbtn oc-copybtn" type="button">Copy</button></div>';
		el.contact.querySelector('[data-c="email"]').onclick = function () { if (emails.length) window.location.href = "mailto:" + emails.join(","); };
		el.contact.querySelector('[data-c="cal"]').onclick = function () { if (emails.length) downloadICS(people); };
		el.contact.querySelector('[data-c="slack"]').onclick = function () {
			var box = el.contact.querySelector(".oc-copybox"), inp = el.contact.querySelector(".oc-copyin");
			inp.value = people.map(handleFor).join(" ");
			box.hidden = false; inp.focus(); inp.select();
		};
		el.contact.querySelector(".oc-copybtn").onclick = function () {
			var inp = el.contact.querySelector(".oc-copyin");
			inp.focus(); inp.select();
			var done = function () { toast("Copied"); };
			if (navigator.clipboard) navigator.clipboard.writeText(inp.value).then(done, function () { try { document.execCommand("copy"); done(); } catch (e) {} });
			else { try { document.execCommand("copy"); done(); } catch (e) {} }
		};
		el.contact.querySelector('[data-c="clear"]').onclick = function () {
			selection.forEach(function (id) { var d = domNodes.get(id); if (d) d.classList.remove("picked"); });
			selection.clear(); updateContactBar();
		};
	}
	var _toastT = null;
	function toast(msg) {
		var t = document.getElementById("oc-toast");
		if (!t) { t = document.createElement("div"); t.id = "oc-toast"; t.className = "oc-toast"; el.stage.appendChild(t); }
		t.textContent = msg; t.classList.add("on");
		if (_toastT) clearTimeout(_toastT);
		_toastT = setTimeout(function () { t.classList.remove("on"); }, 2200);
	}
	function copyText(text, msg) {
		if (navigator.clipboard) navigator.clipboard.writeText(text).then(function () { toast(msg); }, function () { toast(text); });
		else toast(text);
	}
	function wire() {
		el.nodes.addEventListener("click", function (e) {
			var tog = e.target.closest(".oc-toggle"), node = e.target.closest(".oc-node");
			if (!node) return;
			var id = node.dataset.id;
			if (tog) { collapsed.has(id) ? collapsed.delete(id) : collapsed.add(id); render(); return; }
			if (e.shiftKey || e.metaKey || e.ctrlKey) {
				if (selection.has(id)) selection.delete(id); else selection.add(id);
				var dn = domNodes.get(id); if (dn) dn.classList.toggle("picked", selection.has(id));
				updateContactBar();
				return;
			}
			openDetail(id);
		});
		el.search.addEventListener("input", function () { searchIdx = -1; render(); });
		el.search.addEventListener("keydown", function (e) {
			if (e.key !== "Enter") return;
			e.preventDefault();
			var q = el.search.value.trim();
			if (!q) return;
			if (!stepSearch()) toast("No one matches \u201c" + q + "\u201d");
		});
		el.team.addEventListener("change", applyFilters);
		el.topic.addEventListener("change", applyFilters);
		byId("oc-fit", function (b) { b.onclick = fit; });
		byId("oc-zoomin", function (b) { b.onclick = function () { zoomButton(1.2); }; });
		byId("oc-zoomout", function (b) { b.onclick = function () { zoomButton(1 / 1.2); }; });
		byId("oc-expand", function (b) { b.onclick = function () { collapsed.clear(); render(); fit(); }; });
		byId("oc-collapse", function (b) {
			b.onclick = function () { collapsed.clear(); nodes.forEach(function (n, id) { if (parentOf(id) == null && n.childIds.length) collapsed.add(id); }); render(); fit(); };
		});
		byId("oc-myteam", function (b) {
			var me = (document.body.getAttribute("data-me") || "").trim();
			if (!me) { b.style.display = "none"; return; }
			b.onclick = function () { jumpToMe(me); };
		});
		document.addEventListener("keydown", function (e) { if (e.key === "Escape") closeDetail(); });
		document.addEventListener("click", function (e) {
			if (e.target.closest(".d-menu")) return;
			var m = document.querySelector(".d-menu-list"); if (m && !m.hidden) m.hidden = true;
		});

		var drag = null;
		el.stage.addEventListener("mousedown", function (e) {
			if (e.target.closest(".oc-node") || e.target.closest(".oc-detail")) return;
			drag = { x: e.clientX, y: e.clientY, tx: view.tx, ty: view.ty };
			el.stage.classList.add("panning");
		});
		window.addEventListener("mousemove", function (e) {
			if (!drag) return;
			view.tx = drag.tx + (e.clientX - drag.x); view.ty = drag.ty + (e.clientY - drag.y); applyView();
		});
		window.addEventListener("mouseup", function () { drag = null; el.stage.classList.remove("panning"); });
		el.stage.addEventListener("wheel", function (e) {
			e.preventDefault();
			// A pinch on a trackpad arrives as a wheel event with ctrlKey set, and
			// that is the only wheel gesture that means zoom. A plain two-finger
			// scroll means pan. Treating every wheel event as a zoom sent the chart
			// flying, because one scroll delivers dozens of them and each one
			// multiplied the scale again.
			var r = el.stage.getBoundingClientRect();
			if (e.ctrlKey || e.metaKey) {
				// Scale by how far the gesture actually moved, gently. A pinch
				// delivers a stream of events, so a per-event step that feels right
				// on its own compounds into a lurch across the whole gesture: the
				// delta is capped and damped so the zoom tracks the fingers.
				var step = Math.max(-ZOOM_STEP_CAP, Math.min(ZOOM_STEP_CAP, e.deltaY));
				zoomAround(e.clientX - r.left, e.clientY - r.top, Math.exp(-step * ZOOM_SENSITIVITY));
				return;
			}
			view.tx -= e.deltaX;
			view.ty -= e.deltaY;
			applyView();
		}, { passive: false });
	}

	function showEmpty(msg) { el.empty.hidden = false; el.empty.textContent = msg; }
	window.addEventListener("error", function (e) { showEmpty("Script error: " + (e.message || (e.error && e.error.message))); });

	function init() {
		wire();
		api("/api/directory").then(function (dir) {
			if (!dir.people || !dir.people.length) { showEmpty("Nothing indexed yet. Run whodar index against a source, then reload."); return; }
			buildGraph(dir);
			populateFilters(dir);
			render();
			fit();
			if (positions.size === 0) showEmpty("Loaded " + dir.people.length + " people but placed no nodes.");
		}).catch(function (err) { showEmpty("Could not load the directory: " + (err && err.message ? err.message : err)); });
	}
	init();
})();
