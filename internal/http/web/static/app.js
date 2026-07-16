// Sextant console - minimal progressive enhancement. Self-hosted, no
// framework, CSP-clean (default-src 'self'). The console works fully
// without JS; this only adds convenience.

// Copy-to-clipboard, two forms: an element with data-copy="<text>" copies
// that text on click; a bare data-copy (no value) copies the element's own
// text. A [data-copy-btn] copies the nearest [data-copy] in the same parent
// (the reveal page's code + button pair). The value stays selectable in the
// DOM, so no-JS users can still select it by hand.
document.addEventListener("click", function (e) {
  var el = e.target.closest("[data-copy]");
  var btn = e.target.closest("[data-copy-btn]");
  if (!el && btn) {
    el = btn.parentElement && btn.parentElement.querySelector("[data-copy]");
  }
  if (!el) return;
  var text = el.getAttribute("data-copy") || el.textContent;
  if (!navigator.clipboard) return;
  navigator.clipboard.writeText(text).then(function () {
    var target = btn || el;
    var label = target.getAttribute("data-copied") || "Copied";
    var prev = target.getAttribute("data-label");
    if (prev === null) target.setAttribute("data-label", target.textContent);
    target.textContent = label;
    setTimeout(function () {
      target.textContent = target.getAttribute("data-label");
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
// Drives the styled #confirm-modal in layout.html instead of the native
// window.confirm(), which looked out of place. Falls back to window.confirm if
// the markup is missing so a destructive action is never silently unguarded.
(function () {
  var pendingForm = null, pendingSubmitter = null;
  var modal = null, msgEl = null;

  function ready() {
    if (!modal) { modal = document.getElementById("confirm-modal"); msgEl = document.getElementById("confirm-msg"); }
    return modal && msgEl;
  }
  function open(msg, form, submitter) {
    if (!ready()) { if (window.confirm(msg)) form.submit(); return; }
    pendingForm = form; pendingSubmitter = submitter;
    msgEl.textContent = msg;
    modal.classList.remove("hidden"); modal.classList.add("flex");
    var ok = document.getElementById("confirm-ok"); if (ok) ok.focus();
  }
  function close() {
    if (modal) { modal.classList.add("hidden"); modal.classList.remove("flex"); }
    pendingForm = null; pendingSubmitter = null;
  }

  document.addEventListener("submit", function (e) {
    var f = e.target;
    var el = (e.submitter && e.submitter.hasAttribute("data-confirm")) ? e.submitter
      : (f && f.matches("[data-confirm]") ? f : null);
    if (!el) return;
    e.preventDefault();
    open(el.getAttribute("data-confirm"), f, e.submitter);
  });

  document.addEventListener("click", function (e) {
    if (e.target.closest("#confirm-cancel")) { close(); return; }
    if (e.target.closest("#confirm-ok")) {
      var form = pendingForm, sub = pendingSubmitter;
      close();
      if (form) {
        // form.submit() bypasses the submit event (and this guard). Re-attach the
        // triggering button's name/value so multi-button forms still post it.
        if (sub && sub.name) {
          var h = document.createElement("input");
          h.type = "hidden"; h.name = sub.name; h.value = sub.value;
          form.appendChild(h);
        }
        form.submit();
      }
      return;
    }
    if (modal && e.target === modal) close(); // backdrop click cancels
  });

  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape" && modal && !modal.classList.contains("hidden")) close();
  });
})();

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

// Device-list multi-select: tick rows to reveal the selection bar (#sel-bar),
// then "create group from selection" posts the ticked tags with a new group
// name. Delegated, so it survives any re-render of the table.
(function () {
  function selected() {
    return Array.prototype.slice.call(document.querySelectorAll(".dev-select:checked"));
  }
  function sync() {
    var bar = document.getElementById("sel-bar");
    if (!bar) return;
    var boxes = document.querySelectorAll(".dev-select");
    var n = selected().length;
    var count = document.getElementById("sel-count");
    if (count) count.textContent = String(n);
    if (n > 0) { bar.classList.remove("hidden"); bar.classList.add("flex"); }
    else { bar.classList.add("hidden"); bar.classList.remove("flex"); }
    var all = document.getElementById("sel-all");
    if (all) {
      all.checked = boxes.length > 0 && n === boxes.length;
      all.indeterminate = n > 0 && n < boxes.length;
    }
  }
  document.addEventListener("change", function (e) {
    if (e.target.id === "sel-all") {
      var on = e.target.checked;
      document.querySelectorAll(".dev-select").forEach(function (b) { b.checked = on; });
      sync();
    } else if (e.target.classList && e.target.classList.contains("dev-select")) {
      sync();
    }
  });
  document.addEventListener("submit", function (e) {
    if (e.target.id !== "sel-group-form") return;
    var form = e.target;
    form.querySelectorAll("input[data-sel-tag]").forEach(function (n) { n.remove(); });
    var tags = selected();
    if (tags.length === 0) { e.preventDefault(); return; }
    tags.forEach(function (b) {
      var h = document.createElement("input");
      h.type = "hidden"; h.name = "tags"; h.value = b.value;
      h.setAttribute("data-sel-tag", "1");
      form.appendChild(h);
    });
  });
})();
