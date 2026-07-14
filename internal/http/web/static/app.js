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

// Confirm guard: any <form data-confirm="message"> asks for confirmation before
// it submits. Delegated so it also covers forms rendered later. Replaces inline
// onsubmit="return confirm(...)" handlers, which the strict CSP (no
// 'unsafe-inline') silently disabled - so destructive actions (retire, remove,
// wipe, reboot, reject, cancel) prompt again.
//
// The SUBMITTER button is checked first, not just the form: a form with
// several submit buttons that each set their own formaction (e.g. one
// "remove" button per row in a batch form) needs a per-button message, which
// a form-level data-confirm cannot express. e.submitter is the button/input
// that triggered this particular submit (native, works even for a
// Enter-key submit as long as a submit control has focus); it falls back to
// the form itself for every existing single-purpose data-confirm form.
document.addEventListener("submit", function (e) {
  var f = e.target;
  var el = (e.submitter && e.submitter.hasAttribute("data-confirm")) ? e.submitter
    : (f && f.matches("[data-confirm]") ? f : null);
  if (el && !window.confirm(el.getAttribute("data-confirm"))) {
    e.preventDefault();
  }
});

// Configuration editor: guard against losing unsaved edits when switching
// scope. A setting value is "dirty" once its input changes and before its row
// Save is used; switching group/device (a plain GET navigation) would discard
// it silently. Track the dirty state and confirm before navigating away.
(function () {
  var dirty = false;

  // Any change inside the settings form (value control or enforce box) marks
  // the editor dirty. The scope selector is a separate form, so switching scope
  // does not itself count as an unsaved edit.
  function mark(e) {
    if (e.target && e.target.closest && e.target.closest("[data-dirty-form]")) {
      dirty = true;
    }
  }
  document.addEventListener("input", mark);
  document.addEventListener("change", mark);
  // Save submits the whole form and persists everything - not a loss.
  document.addEventListener("submit", function () {
    dirty = false;
  });
  // Any other navigation away (closing the tab, a link, the browser back
  // button) with unsaved edits gets the browser's native "leave site?" prompt.
  window.addEventListener("beforeunload", function (e) {
    if (dirty) {
      e.preventDefault();
      e.returnValue = "";
    }
  });

  var warn =
    "You have unsaved changes at this scope. Switch anyway and discard them?";

  // The scope drill-down selects (group, device) navigate on change; the org
  // link navigates on click. Both confirm first when there are unsaved edits,
  // then clear the flag so the intended navigation is not double-prompted.
  document.querySelectorAll("[data-scope-select]").forEach(function (sel) {
    sel.addEventListener("change", function () {
      if (dirty && !window.confirm(warn)) {
        sel.value = sel.getAttribute("data-current") || "";
        return;
      }
      dirty = false;
      sel.form.submit();
    });
  });
  document.addEventListener("click", function (e) {
    var link = e.target.closest("[data-scope-link]");
    if (link && dirty && !window.confirm(warn)) e.preventDefault();
  });
})();

// Account menu (and any <details data-menu>): the native disclosure opens and
// closes on the summary already; this only adds the expected "click outside to
// close" and Escape-to-close, so it behaves like a dropdown.
(function () {
  document.addEventListener("click", function (e) {
    document.querySelectorAll("details[data-menu][open]").forEach(function (d) {
      if (!d.contains(e.target)) d.removeAttribute("open");
    });
  });
  document.addEventListener("keydown", function (e) {
    if (e.key !== "Escape") return;
    document.querySelectorAll("details[data-menu][open]").forEach(function (d) {
      d.removeAttribute("open");
    });
  });
})();
