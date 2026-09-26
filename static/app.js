// Adds the cook's local date to season/ingredient requests and handles
// "Use my location" with a search fallback.
(function () {
  function localDate() {
    var d = new Date();
    return d.getFullYear() + "-" + String(d.getMonth() + 1).padStart(2, "0") + "-" + String(d.getDate()).padStart(2, "0");
  }
  document.addEventListener("htmx:configRequest", function (e) {
    var p = e.detail.path || "";
    if ((p.indexOf("/season") === 0 || p.indexOf("/ingredient/") === 0) && !e.detail.parameters.date) {
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
