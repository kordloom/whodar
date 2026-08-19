// whodar theme engine (landing). Themes are CSS token sets selected by
// data-theme on the root: radar (default), ink (black), loom (white). Runs in
// <head> before paint to avoid a flash of the wrong theme, and syncs every
// [data-theme-btn] swatch on the page.
(function () {
	"use strict";
	var KEY = "whodar-theme";
	var THEMES = ["radar", "ink", "loom"];
	var DEFAULT = "radar";

	function current() {
		try {
			var t = localStorage.getItem(KEY);
			return THEMES.indexOf(t) >= 0 ? t : DEFAULT;
		} catch (e) { return DEFAULT; }
	}
	function mark(t) {
		var btns = document.querySelectorAll("[data-theme-btn]");
		for (var i = 0; i < btns.length; i++) {
			var on = btns[i].getAttribute("data-theme-btn") === t;
			btns[i].classList.toggle("on", on);
			btns[i].setAttribute("aria-pressed", on ? "true" : "false");
		}
	}
	function apply(t) {
		if (THEMES.indexOf(t) < 0) t = DEFAULT;
		document.documentElement.setAttribute("data-theme", t);
		try { localStorage.setItem(KEY, t); } catch (e) { /* private mode */ }
		mark(t);
	}

	document.documentElement.setAttribute("data-theme", current());

	function wire() {
		var btns = document.querySelectorAll("[data-theme-btn]");
		for (var i = 0; i < btns.length; i++) {
			(function (b) { b.addEventListener("click", function () { apply(b.getAttribute("data-theme-btn")); }); })(btns[i]);
		}
		mark(current());
	}
	if (document.readyState !== "loading") wire();
	else document.addEventListener("DOMContentLoaded", wire);

	window.whodarTheme = { apply: apply, current: current, themes: THEMES };
})();
