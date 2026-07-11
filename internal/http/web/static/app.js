// Sextant console — minimal progressive enhancement. Self-hosted, no
// framework, CSP-clean (default-src 'self'). The console works fully
// without JS; this only adds convenience.

// Copy-to-clipboard: any element with data-copy="<text>" copies it on
// click and briefly confirms. The copied value stays selectable in the
// DOM, so no-JS users can still select it by hand.
document.addEventListener("click", function (e) {
  var el = e.target.closest("[data-copy]");
  if (!el) return;
  var text = el.getAttribute("data-copy");
  if (!navigator.clipboard) return;
  navigator.clipboard.writeText(text).then(function () {
    var label = el.getAttribute("data-copied") || "Copied";
    var prev = el.getAttribute("data-label");
    if (prev === null) el.setAttribute("data-label", el.textContent);
    el.textContent = label;
    setTimeout(function () {
      el.textContent = el.getAttribute("data-label");
    }, 1200);
  });
});
