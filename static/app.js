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
  function requestPath(e) {
    var d = e && e.detail || {};
    var cfg = d.requestConfig || {};
    var path = (d.pathInfo && (d.pathInfo.requestPath || d.pathInfo.path)) || cfg.path || "";
    if (path && path.indexOf("?") >= 0) return path;
    var params = cfg.parameters || {};
    var keys = Object.keys(params);
    if (!path || !keys.length) return path;
    var qs = keys.map(function (k) {
      return encodeURIComponent(k) + "=" + encodeURIComponent(params[k]);
    }).join("&");
    return path + "?" + qs;
  }
  function writeInline(t, msg, href) {
    if (!t) return;
    t.replaceChildren();
    var host;
    if (t.id === "places") {
      host = document.createElement("p");
      host.className = "hint";
      host.setAttribute("data-announce", msg);
      host.appendChild(document.createTextNode(msg));
      t.appendChild(host);
    } else {
      host = document.createElement("div");
      host.className = "panel warn";
      host.setAttribute("data-announce", msg);
      var p = document.createElement("p");
      p.appendChild(document.createTextNode(msg));
      host.appendChild(p);
      t.appendChild(host);
    }
    if (!href) return;
    var wrap = document.createElement("p");
    var a = document.createElement("a");
    a.setAttribute("href", href);
    a.setAttribute("hx-get", href);
    a.setAttribute("hx-target", t.id === "places" ? "#places" : "#results");
    a.appendChild(document.createTextNode("Try again"));
    wrap.appendChild(a);
    host.appendChild(wrap);
    htmx.process(t);
  }
  function focusHeading(t) {
    var h = t && t.querySelector(".where h2, h2, h3");
    if (!h) return;
    if (!h.hasAttribute("tabindex")) h.setAttribute("tabindex", "-1");
    h.focus();
  }
  function restoreFocus(t) {
    if (!t) return;
    var active = document.activeElement;
    if (active && active !== document.body && !t.contains(active)) return;
    if (t.id === "results") {
      var place = document.querySelector("#places a");
      if (place) {
        place.focus();
        return;
      }
      focusHeading(t);
      return;
    }
    var link = t.querySelector("a");
    if (link) {
      link.focus();
      return;
    }
    var q = searchBox();
    if (q) q.focus();
  }
  function announceSwap(e) {
    var t = swapTarget(e.detail && e.detail.target);
    announceFrom(t);
    restoreFocus(t);
  }
  function onTransportError(e) {
    var t = swapTarget(e.detail && e.detail.target);
    setBusy(t, false);
    writeInline(t, networkCopy(t), requestPath(e));
    announceFrom(t);
    if (t && t.id === "places") {
      var q = searchBox();
      if (q) q.focus();
    } else {
      restoreFocus(t);
    }
  }
  function onResponseError(e) {
    var t = swapTarget(e.detail && e.detail.target);
    setBusy(t, false);
    var xhr = e.detail && e.detail.xhr;
    var body = xhr && xhr.responseText;
    if (!body || !String(body).replace(/\s/g, "")) {
      writeInline(t, networkCopy(t), requestPath(e));
      announceFrom(t);
    }
  }
  htmx.config.timeout = 8000;
  document.addEventListener("htmx:beforeRequest", function (e) { setBusy(swapTarget(e.detail && e.detail.target), true); });
  document.addEventListener("htmx:afterRequest", function (e) { setBusy(swapTarget(e.detail && e.detail.target), false); });
  document.addEventListener("htmx:afterSwap", announceSwap);
  document.addEventListener("htmx:sendError", onTransportError);
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
