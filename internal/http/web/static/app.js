// Sextant console - minimal progressive enhancement. Self-hosted, no
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

// Configuration editor: guard against losing unsaved edits when switching
// scope. A setting value is "dirty" once its input changes and before its row
// Save is used; switching group/device (a plain GET navigation) would discard
// it silently. Track the dirty state and confirm before navigating away.
(function () {
  var dirty = false;

  // Any change to a per-row value input marks the editor dirty.
  document.addEventListener("input", function (e) {
    if (e.target && e.target.name === "value") dirty = true;
  });
  // A row's own Save/Clear submit persists (or clears) the value, so leaving
  // via that form is not a loss.
  document.addEventListener("submit", function () {
    dirty = false;
  });

  var warn =
    "You have unsaved changes at this scope. Switch anyway and discard them?";

  // The scope drill-down selects (group, device) navigate on change; the org
  // link navigates on click. Both confirm first when there are unsaved edits.
  document.querySelectorAll("[data-scope-select]").forEach(function (sel) {
    sel.addEventListener("change", function () {
      if (dirty && !window.confirm(warn)) {
        sel.value = sel.getAttribute("data-current") || "";
        return;
      }
      sel.form.submit();
    });
  });
  document.addEventListener("click", function (e) {
    var link = e.target.closest("[data-scope-link]");
    if (link && dirty && !window.confirm(warn)) e.preventDefault();
  });
})();
