// Nav: solid once scrolled.
(function () {
	var nav = document.getElementById("nav");
	var onScroll = function () { nav.classList.toggle("scrolled", window.scrollY > 8); };
	onScroll();
	window.addEventListener("scroll", onScroll, { passive: true });
})();

// Top-left menu drawer: contact and docs. Toggles on click, closes on an
// outside click or Escape.
(function () {
	var btn = document.getElementById("menu-btn");
	var drawer = document.getElementById("menu-drawer");
	if (!btn || !drawer) return;
	function close() { drawer.hidden = true; btn.setAttribute("aria-expanded", "false"); }
	function open() { drawer.hidden = false; btn.setAttribute("aria-expanded", "true"); }
	drawer.querySelectorAll("a").forEach(function (a) { a.addEventListener("click", function () { close(); }); });
	btn.addEventListener("click", function (e) {
		e.stopPropagation();
		if (drawer.hidden) { open(); } else { close(); }
	});
	document.addEventListener("click", function (e) {
		if (!drawer.hidden && !drawer.contains(e.target)) { close(); }
	});
	document.addEventListener("keydown", function (e) { if (e.key === "Escape") { close(); } });
})();

// Reveal on scroll.
(function () {
	var els = document.querySelectorAll(".reveal");
	if (!("IntersectionObserver" in window)) { els.forEach(function (el) { el.classList.add("in"); }); return; }
	var io = new IntersectionObserver(function (entries) {
		entries.forEach(function (e) { if (e.isIntersecting) { e.target.classList.add("in"); io.unobserve(e.target); } });
		// Fire a section's fade before it reaches the viewport, not twelve
		// percent into it. A fast scroller was meeting a blank band where the
		// content sat at opacity zero waiting to be noticed.
	}, { threshold: 0, rootMargin: "0px 0px 25% 0px" });
	els.forEach(function (el) { io.observe(el); });
})();

// Copy buttons.
(function () {
	document.querySelectorAll(".copy").forEach(function (btn) {
		btn.addEventListener("click", function () {
			var pre = btn.parentElement.querySelector("pre");
			if (!pre) return;
			navigator.clipboard.writeText(pre.innerText).then(function () {
				btn.textContent = "Copied"; btn.classList.add("done");
				setTimeout(function () { btn.textContent = "Copy"; btn.classList.remove("done"); }, 1600);
			}).catch(function () { btn.textContent = "Press ⌘C"; });
		});
	});
})();

// Get-whodar guide: switch the install/use pane from the left menu.
(function () {
	var menu = document.querySelector(".guide-menu");
	if (!menu) return;
	var btns = menu.querySelectorAll(".gm");
	var panes = document.querySelectorAll(".guide-pane .gp");
	menu.addEventListener("click", function (e) {
		var b = e.target.closest(".gm");
		if (!b) return;
		btns.forEach(function (x) { x.classList.toggle("on", x === b); });
		panes.forEach(function (p) { p.classList.toggle("on", p.dataset.pane === b.dataset.tab); });
	});
})();

// Radar scope: a phosphor sweep locates contacts. Distance from center is
// confidence, so the strongest match sits nearest the middle.
(function () {
	var scope = document.getElementById("scope");
	if (!scope) return;
	var canvas = document.getElementById("scopeCanvas");
	var ctx = canvas.getContext("2d");
	var reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

	// angle in radians (math convention, y flipped on screen), r as a fraction
	// of the scope radius derived from confidence: nearer center is stronger.
	var contacts = [
		{ el: "c0", ang: -1.95, conf: 0.98, locked: false },
		{ el: "c1", ang: 0.55, conf: 0.62, locked: false },
		{ el: "c2", ang: 2.45, conf: 0.31, locked: false }
	];
	contacts.forEach(function (c) { c.node = document.getElementById(c.el); c.r = 0.16 + (1 - c.conf) * 0.62; });

	// Fixed background noise: the rest of the org, dim contacts in the sweep.
	var noise = [];
	var seed = [12, 47, 88, 121, 156, 193, 214, 248, 271, 299, 332, 355, 25, 70, 140, 205, 285, 320];
	seed.forEach(function (deg, i) {
		noise.push({ ang: deg * Math.PI / 180, r: 0.28 + ((i * 53) % 63) / 100 });
	});

	var size = 0, cx = 0, cy = 0, R = 0, dpr = 1;
	function resize() {
		dpr = window.devicePixelRatio || 1;
		size = scope.clientWidth;
		canvas.width = size * dpr; canvas.height = size * dpr;
		ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
		cx = size / 2; cy = size / 2; R = size * 0.44;
		contacts.forEach(function (c) {
			var x = cx + Math.cos(c.ang) * c.r * R;
			var y = cy + Math.sin(c.ang) * c.r * R;
			c.node.style.left = x + "px"; c.node.style.top = y + "px";
		});
	}

	function ring(rr) { ctx.beginPath(); ctx.arc(cx, cy, R * rr, 0, Math.PI * 2); ctx.strokeStyle = "rgba(120,160,210,0.14)"; ctx.lineWidth = 1; ctx.stroke(); }
	function blip(ang, r, a, big) {
		var x = cx + Math.cos(ang) * r * R, y = cy + Math.sin(ang) * r * R;
		ctx.beginPath(); ctx.arc(x, y, big ? 3.4 : 2, 0, Math.PI * 2);
		ctx.fillStyle = "rgba(52,229,197," + a + ")"; ctx.fill();
		if (big && a > 0.6) { ctx.beginPath(); ctx.arc(x, y, 7, 0, Math.PI * 2); ctx.strokeStyle = "rgba(52,229,197," + (a * 0.5) + ")"; ctx.lineWidth = 1; ctx.stroke(); }
	}
	// Brightness of a point given how recently the sweep passed it.
	function glow(ang, base) {
		var d = (sweep - ang) % (Math.PI * 2); if (d < 0) d += Math.PI * 2;
		var trail = 1.15;
		return d < trail ? Math.max(base, 1 - d / trail) : base;
	}

	var sweep = -1.95;
	function frame(dt) {
		ctx.clearRect(0, 0, size, size);
		ring(0.32); ring(0.55); ring(0.78); ring(1.0);
		// cross axes
		ctx.strokeStyle = "rgba(120,160,210,0.10)"; ctx.lineWidth = 1;
		ctx.beginPath(); ctx.moveTo(cx - R, cy); ctx.lineTo(cx + R, cy); ctx.moveTo(cx, cy - R); ctx.lineTo(cx, cy + R); ctx.stroke();
		// bearing ticks
		for (var k = 0; k < 24; k++) {
			var a = k * Math.PI / 12, inner = k % 6 === 0 ? 0.9 : 0.95;
			ctx.beginPath();
			ctx.moveTo(cx + Math.cos(a) * R * inner, cy + Math.sin(a) * R * inner);
			ctx.lineTo(cx + Math.cos(a) * R, cy + Math.sin(a) * R);
			ctx.strokeStyle = "rgba(120,160,210,0.2)"; ctx.stroke();
		}
		// sweep wedge + leading line
		if (!reduce) {
			ctx.save(); ctx.translate(cx, cy); ctx.rotate(sweep);
			ctx.beginPath(); ctx.moveTo(0, 0); ctx.arc(0, 0, R, -1.15, 0); ctx.closePath();
			ctx.fillStyle = "rgba(52,229,197,0.09)"; ctx.fill();
			ctx.beginPath(); ctx.moveTo(0, 0); ctx.lineTo(R, 0);
			ctx.strokeStyle = "rgba(52,229,197,0.85)"; ctx.lineWidth = 1.6;
			ctx.shadowBlur = 12; ctx.shadowColor = "rgba(52,229,197,0.8)"; ctx.stroke(); ctx.shadowBlur = 0;
			ctx.restore();
		}
		// noise + contacts
		noise.forEach(function (n) { blip(n.ang, n.r, glow(n.ang, 0.1) * 0.7); });
		contacts.forEach(function (c) {
			var b = c.locked ? 0.9 : glow(c.ang, 0.28);
			blip(c.ang, c.r, b, true);
			if (!c.locked) {
				var d = (sweep - c.ang) % (Math.PI * 2); if (d < 0) d += Math.PI * 2;
				if (d < 0.08) { c.locked = true; c.node.classList.add("on"); }
			}
		});
		if (!reduce) { sweep += 0.017; if (sweep > Math.PI) sweep -= Math.PI * 2; requestAnimationFrame(frame); }
	}

	function start() {
		resize();
		if (reduce) { sweep = 5; contacts.forEach(function (c) { c.locked = true; c.node.classList.add("on"); }); frame(); return; }
		requestAnimationFrame(frame);
	}

	window.addEventListener("resize", function () { resize(); if (reduce) frame(); });

	var started = false;
	if ("IntersectionObserver" in window) {
		var io = new IntersectionObserver(function (entries) {
			entries.forEach(function (e) { if (e.isIntersecting && !started) { started = true; start(); io.disconnect(); } });
		}, { threshold: 0.25 });
		io.observe(scope);
	} else { start(); }
})();
