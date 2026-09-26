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
  document.addEventListener("click", function (e) {
    var btn = e.target.closest("[data-geolocate]");
    if (!btn) return;
    var hint = document.getElementById("geo-hint");
    var search = document.getElementById("q");
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
})();
