// Admin: WebAuthn ceremonies over JSON, CSRF header on every POST.
(function () {
  var b64u = {
    enc: function (buf) {
      var s = "", bytes = new Uint8Array(buf);
      for (var i = 0; i < bytes.length; i++) s += String.fromCharCode(bytes[i]);
      return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
    },
    dec: function (s) {
      s = s.replace(/-/g, "+").replace(/_/g, "/");
      while (s.length % 4) s += "=";
      return Uint8Array.from(atob(s), function (c) { return c.charCodeAt(0); }).buffer;
    }
  };
  function csrf() { var m = document.querySelector('meta[name="csrf-token"]'); return m ? m.content : ""; }
  async function post(url, body) {
    var r = await fetch(url, {
      method: "POST", credentials: "same-origin",
      headers: { "X-CSRF-Token": csrf(), "Content-Type": "application/json" },
      body: JSON.stringify(body || {})
    });
    var j = {};
    try { j = await r.json(); } catch (e) { /* non-JSON error */ }
    if (!r.ok) throw new Error(j.error || "Something went wrong. Try again.");
    return j;
  }
  async function login(begin, finish) {
    var pk = (await post(begin)).publicKey;
    pk.challenge = b64u.dec(pk.challenge);
    (pk.allowCredentials || []).forEach(function (c) { c.id = b64u.dec(c.id); });
    var cred = await navigator.credentials.get({ publicKey: pk });
    return post(finish, {
      id: cred.id, rawId: b64u.enc(cred.rawId), type: cred.type,
      response: {
        clientDataJSON: b64u.enc(cred.response.clientDataJSON),
        authenticatorData: b64u.enc(cred.response.authenticatorData),
        signature: b64u.enc(cred.response.signature),
        userHandle: cred.response.userHandle ? b64u.enc(cred.response.userHandle) : null
      }
    });
  }
  async function register(begin, finish) {
    var pk = (await post(begin)).publicKey;
    pk.challenge = b64u.dec(pk.challenge);
    pk.user.id = b64u.dec(pk.user.id);
    (pk.excludeCredentials || []).forEach(function (c) { c.id = b64u.dec(c.id); });
    var cred = await navigator.credentials.create({ publicKey: pk });
    return post(finish, {
      id: cred.id, rawId: b64u.enc(cred.rawId), type: cred.type,
      response: {
        clientDataJSON: b64u.enc(cred.response.clientDataJSON),
        attestationObject: b64u.enc(cred.response.attestationObject),
        transports: cred.response.getTransports ? cred.response.getTransports() : []
      }
    });
  }
  document.addEventListener("click", async function (e) {
    var b = e.target.closest("[data-webauthn]");
    if (!b) return;
    e.preventDefault();
    var status = document.getElementById("wa-status");
    try {
      var finish = b.dataset.finish;
      if (b.dataset.nameInput) {
        var n = document.getElementById(b.dataset.nameInput).value.trim() || "Passkey";
        finish += (finish.indexOf("?") >= 0 ? "&" : "?") + "name=" + encodeURIComponent(n);
      }
      var res = b.dataset.webauthn === "register" ? await register(b.dataset.begin, finish) : await login(b.dataset.begin, finish);
      location.assign(res.redirect || location.href);
    } catch (err) {
      if (status) status.textContent = err.message || String(err);
    }
  });
})();
