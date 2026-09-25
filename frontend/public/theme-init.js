// Synchronous theme bootstrap to avoid a flash of the wrong theme (FOUC).
// Loaded as a classic, render-blocking script from <head> so it runs before
// first paint. It lives in its own file rather than inline in index.html so
// the Content-Security-Policy can forbid inline scripts (script-src 'self').
// Mirror of frontend/src/lib/use-theme.ts (intentional duplication: must run
// before the module bundle loads).
(function () {
  try {
    var pref = localStorage.getItem("kafkito.theme") || "system";
    if (pref !== "light" && pref !== "dark") {
      pref = window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
    }
    document.documentElement.classList.toggle("dark", pref === "dark");
  } catch (_) {
    /* default light */
  }
})();
