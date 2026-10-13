// Sets the cook's local date on season/ingredient requests (overriding any
// date already in the URL, e.g. from a bookmark) and handles "Use my
// location" with a search fallback.
(function () {
  function localDate() {
    var d = new Date();
    return d.getFullYear() + "-" + String(d.getMonth() + 1).padStart(2, "0") + "-" + String(d.getDate()).padStart(2, "0");
  }
  // htmx appends parameters to the path's own query string, so a date already
  // in the path must be removed or the server would see the stale one first.
  function withoutDate(path) {
    var hash = path.indexOf("#");
    var frag = hash < 0 ? "" : path.slice(hash);
    var rest = hash < 0 ? path : path.slice(0, hash);
    var q = rest.indexOf("?");
    if (q < 0) return path;
    var params = new URLSearchParams(rest.slice(q + 1));
    params.delete("date");
    var qs = params.toString();
    return rest.slice(0, q) + (qs ? "?" + qs : "") + frag;
  }
  document.addEventListener("htmx:configRequest", function (e) {
    var p = e.detail.path || "";
    if (p.indexOf("/season") === 0 || p.indexOf("/ingredient/") === 0) {
      e.detail.path = withoutDate(p);
      e.detail.parameters.date = localDate();
    }
  });
  function searchBox() { return document.getElementById("q"); }
  function focusSearch() {
    var search = searchBox();
    if (search && location.hash === "#q") search.focus();
  }
  function swapTarget(el) {
    return el && (el.id === "places" || el.id === "results") ? el : null;
  }
  function setBusy(t, on) {
    if (!t) return;
    if (on) t.setAttribute("aria-busy", "true");
    else t.removeAttribute("aria-busy");
  }
  function announceFrom(t) {
    var announce = document.getElementById("announce");
    if (!announce || !t) return;
    var src = t.querySelector("[data-announce]");
    var msg = src ? src.getAttribute("data-announce") || "" : "";
    announce.textContent = "";
    announce.textContent = msg;
  }
  function networkCopy(t) {
    return t && t.id === "places"
      ? "Search isn't available right now. Try again in a moment."
      : "Couldn't load seasonal picks right now. Try again in a moment.";
  }
  function writeInline(t, msg) {
    if (!t) return;
    if (t.id === "places") {
      t.innerHTML = '<p class="hint" data-announce="' + msg + '">' + msg + "</p>";
    } else {
      t.innerHTML = '<div class="panel warn" data-announce="' + msg + '"><p>' + msg + "</p></div>";
    }
  }
  function announceSwap(e) {
    announceFrom(swapTarget(e.detail && e.detail.target));
  }
  function onTransportError(e) {
    var t = swapTarget(e.detail && e.detail.target);
    setBusy(t, false);
    writeInline(t, networkCopy(t));
    announceFrom(t);
  }
  function onResponseError(e) {
    var t = swapTarget(e.detail && e.detail.target);
    setBusy(t, false);
    var xhr = e.detail && e.detail.xhr;
    var body = xhr && xhr.responseText;
    if (!body || !String(body).replace(/\s/g, "")) writeInline(t, networkCopy(t));
    announceFrom(t);
  }
  htmx.config.timeout = 8000;
  document.addEventListener("htmx:beforeRequest", function (e) { setBusy(swapTarget(e.detail && e.detail.target), true); });
  document.addEventListener("htmx:afterRequest", function (e) { setBusy(swapTarget(e.detail && e.detail.target), false); });
  document.addEventListener("htmx:afterSwap", announceSwap);
  document.addEventListener("htmx:sendError", onTransportError);
  document.addEventListener("htmx:sendAbort", onTransportError);
  document.addEventListener("htmx:timeout", onTransportError);
  document.addEventListener("htmx:responseError", onResponseError);
  window.addEventListener("hashchange", focusSearch);
  document.addEventListener("click", function (e) {
    var a = e.target.closest("a");
    var href = a && a.getAttribute("href");
    if (href === "/#q" || href === "#q") {
      var jump = searchBox();
      if (jump) jump.focus();
    }
    var btn = e.target.closest("[data-geolocate]");
    if (!btn) return;
    var hint = document.getElementById("geo-hint");
    var search = searchBox();
    function fallback() { hint.hidden = false; search.focus(); }
    if (!navigator.geolocation) { fallback(); return; }
    navigator.geolocation.getCurrentPosition(function (pos) {
      hint.hidden = true;
      // Two decimals (~1 km) is plenty for a 28 km grid and keeps shared URLs coarse.
      var url = "/season?lat=" + pos.coords.latitude.toFixed(2) + "&lng=" + pos.coords.longitude.toFixed(2);
      htmx.ajax("GET", url, { target: "#results", swap: "innerHTML" }).then(function () {
        history.pushState({}, "", url);
      });
    }, fallback, { timeout: 10000, maximumAge: 600000 });
  });
  focusSearch();
})();
