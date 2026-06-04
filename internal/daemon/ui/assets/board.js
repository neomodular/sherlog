// board.js — the Case Board entry point: a tiny hash router (design D7: no router
// library) that dispatches to the view modules and tears down the live SSE stream
// on every navigation so only the visible case streams.

import { renderCases } from "./cases.js";
import { renderDetail, closeStream } from "./detail.js";
import { renderDiff } from "./diff.js";
import { renderStale } from "./stale.js";
import { api } from "./api.js";
import { esc } from "./render.js";

const view = document.getElementById("view");

// setNav highlights the active top-level destination.
function setNav(route) {
  for (const a of document.querySelectorAll(".nav a")) {
    a.classList.toggle("active", a.dataset.route === route);
  }
}

// route parses location.hash and renders the matching view. Recognized routes:
//   #/cases                         → case list
//   #/case/<id>                     → case detail (+ live tail when open)
//   #/case/<id>/diff[/<a>/<b>]      → run comparison
//   #/stale                         → stale probes
async function route() {
  closeStream(); // release any prior case's SSE subscriber before switching views
  const hash = location.hash.replace(/^#\/?/, "");
  const parts = hash.split("/").filter(Boolean);

  if (parts.length === 0 || parts[0] === "cases") {
    setNav("cases");
    return renderCases(view);
  }
  if (parts[0] === "stale") {
    setNav("stale");
    return renderStale(view);
  }
  if (parts[0] === "case" && parts[1]) {
    setNav("cases");
    const id = decodeURIComponent(parts[1]);
    if (parts[2] === "diff") {
      // The diff view needs the session's run list; load it once and hand it over.
      let sess;
      try {
        sess = await api.session(id);
      } catch (e) {
        view.innerHTML = `<p class="error">Could not load case #${esc(id)}: ${esc(e.message)}</p>`;
        return;
      }
      const a = parts[3] ? decodeURIComponent(parts[3]) : "";
      const b = parts[4] ? decodeURIComponent(parts[4]) : "";
      return renderDiff(view, sess, a, b);
    }
    return renderDetail(view, id);
  }

  // Unknown route: fall back to the case list rather than a dead page.
  setNav("cases");
  return renderCases(view);
}

window.addEventListener("hashchange", route);
window.addEventListener("DOMContentLoaded", route);
// DOMContentLoaded may have already fired before this module evaluated (module
// scripts are deferred); run once now so the first paint never waits on an event.
route();
