package admin

import (
	"encoding/base64"
	"net/url"
	"strings"
	"testing"
	"time"

	"thymeandplace/internal/auth"
)

func TestSecurityPageListsPasskeys(t *testing.T) {
	e := newEnv(t, true)
	c, _ := e.enrolled(t)
	rec := c.get("/admin/security")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Phone") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestAddPasskeyNeedsRecentAuth(t *testing.T) {
	e := newEnv(t, true)
	c, phone := e.enrolled(t)
	key := newDevice()
	if rec, err := key.register(c, "/admin/security/passkeys/begin", "/admin/security/passkeys/finish?name=Key"); err != nil || rec.Code != 200 {
		t.Fatalf("add within 5 min: %d %s (err=%v)", rec.Code, rec.Body.String(), err)
	}
	e.clock.add(6 * time.Minute)
	c.get("/admin/security")
	third := newDevice()
	if rec, err := third.register(c, "/admin/security/passkeys/begin", "/admin/security/passkeys/finish?name=Third"); err != nil || rec.Code != 403 {
		t.Fatalf("add after 6 min must need re-auth: %d (err=%v)", rec.Code, err)
	}
	if rec := phone.login(c, "/admin/security/reauth/begin", "/admin/security/reauth/finish"); rec.Code != 200 {
		t.Fatalf("reauth: %d %s", rec.Code, rec.Body.String())
	}
	if rec, err := third.register(c, "/admin/security/passkeys/begin", "/admin/security/passkeys/finish?name=Third"); err != nil || rec.Code != 200 {
		t.Fatalf("add after reauth: %d (err=%v)", rec.Code, err)
	}
}

// Review Focus 1: the only passkey cannot be removed.
func TestRemovePasskey(t *testing.T) {
	e := newEnv(t, true)
	c, _ := e.enrolled(t)
	pks, _ := e.auth.Passkeys(t.Context())
	id := base64.RawURLEncoding.EncodeToString(pks[0].ID)
	rec := c.postForm("/admin/security/passkeys/"+id+"/remove", url.Values{})
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "Add another passkey before removing this one") {
		t.Fatalf("remove only passkey: %d %s", rec.Code, rec.Body.String())
	}
	key := newDevice()
	key.register(c, "/admin/security/passkeys/begin", "/admin/security/passkeys/finish?name=Key")
	e.clock.add(6 * time.Minute)
	c.get("/admin/security")
	if rec := c.postForm("/admin/security/passkeys/"+id+"/remove", url.Values{}); rec.Code != 403 {
		t.Fatalf("remove needs re-auth: %d", rec.Code)
	}
	key.login(c, "/admin/security/reauth/begin", "/admin/security/reauth/finish")
	if rec := c.postForm("/admin/security/passkeys/"+id+"/remove", url.Values{}); rec.Code != 303 {
		t.Fatalf("remove after reauth: %d %s", rec.Code, rec.Body.String())
	}
}

func TestRenamePasskey(t *testing.T) {
	e := newEnv(t, true)
	c, _ := e.enrolled(t)
	pks, _ := e.auth.Passkeys(t.Context())
	id := base64.RawURLEncoding.EncodeToString(pks[0].ID)
	e.clock.add(10 * time.Minute) // rename does not need re-auth
	c.get("/admin/security")
	if rec := c.postForm("/admin/security/passkeys/"+id+"/rename", url.Values{"name": {"Work phone"}}); rec.Code != 303 {
		t.Fatalf("rename: %d", rec.Code)
	}
	if rec := c.get("/admin/security"); !strings.Contains(rec.Body.String(), "Work phone") {
		t.Fatal("rename not shown")
	}
}

// Fix round 1, item 1: reauth/finish must be rate limited and audited the
// same way as every other passkey login finish.
func TestReauthFinishIsRateLimitedAndAudited(t *testing.T) {
	e := newEnv(t, true)
	c, _ := e.enrolled(t)
	c.ip = "192.0.2.88"
	c.get("/admin/security")

	rec := c.postJSON("/admin/security/reauth/finish", "not json")
	if rec.Code < 400 || rec.Code >= 500 || !strings.Contains(rec.Body.String(), `"error"`) {
		t.Fatalf("garbage body: %d %s", rec.Code, rec.Body.String())
	}
	var n int
	_ = e.pool.QueryRow(t.Context(), `SELECT count(*) FROM admin_login_log WHERE ip = '192.0.2.88' AND method = 'passkey' AND outcome = 'failure'`).Scan(&n)
	if n != 1 {
		t.Fatalf("failed reauth not audited: n=%d", n)
	}

	for i := 0; i < auth.AttemptLimit-1; i++ {
		c.postJSON("/admin/security/reauth/finish", "not json")
	}
	if rec := c.postJSON("/admin/security/reauth/finish", "not json"); rec.Code != 429 {
		t.Fatalf("6th reauth attempt must be rate limited: %d %s", rec.Code, rec.Body.String())
	}
}

// Fix round 1, item 2: a malformed passkey ID never reaches the store (400,
// no raw decode error); a well-formed but unknown one is a 404. Both show
// the same fixed message.
func TestRenameRemoveUnknownPasskeyID(t *testing.T) {
	e := newEnv(t, true)
	c, _ := e.enrolled(t)
	unknown := base64.RawURLEncoding.EncodeToString([]byte("does-not-exist-cred"))

	if rec := c.postForm("/admin/security/passkeys/A/rename", url.Values{"name": {"x"}}); rec.Code != 400 || !strings.Contains(rec.Body.String(), "Unknown passkey.") {
		t.Fatalf("malformed id rename: %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.postForm("/admin/security/passkeys/"+unknown+"/rename", url.Values{"name": {"x"}}); rec.Code != 404 || !strings.Contains(rec.Body.String(), "Unknown passkey.") {
		t.Fatalf("unknown id rename: %d %s", rec.Code, rec.Body.String())
	}

	if rec := c.postForm("/admin/security/passkeys/A/remove", url.Values{}); rec.Code != 400 || !strings.Contains(rec.Body.String(), "Unknown passkey.") {
		t.Fatalf("malformed id remove: %d %s", rec.Code, rec.Body.String())
	}
	// A removal of an unknown ID while only one passkey exists hits the
	// last-passkey guard before the ID is even looked up (RemovePasskey
	// counts first); register a second passkey so that guard doesn't mask
	// the not-found case being tested here.
	key := newDevice()
	if _, err := key.register(c, "/admin/security/passkeys/begin", "/admin/security/passkeys/finish?name=Key"); err != nil {
		t.Fatal(err)
	}
	if rec := c.postForm("/admin/security/passkeys/"+unknown+"/remove", url.Values{}); rec.Code != 404 || !strings.Contains(rec.Body.String(), "Unknown passkey.") {
		t.Fatalf("unknown id remove: %d %s", rec.Code, rec.Body.String())
	}
}

// Final review items 2 and 3: add-passkey errors are logged with the request
// ID, and the finish route clears the WebAuthn state cookie on failure too.
func TestAddPasskeyErrorsLoggedAndStateCleared(t *testing.T) {
	e := newEnv(t, true)
	c, _ := e.enrolled(t)
	if rec := c.postJSON("/admin/security/passkeys/begin", "{}"); rec.Code != 200 || c.jar[auth.StateCookie] == "" {
		t.Fatalf("add begin: %d", rec.Code)
	}
	rec := c.postJSON("/admin/security/passkeys/finish?name=Key", "not json")
	if rec.Code != 400 {
		t.Fatalf("garbage add finish: %d", rec.Code)
	}
	cleared := false
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == auth.StateCookie && ck.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("failed add finish left the WebAuthn state cookie in place")
	}
	if logs := e.logs.String(); !strings.Contains(logs, "add passkey finish failed") {
		t.Fatalf("add finish failure not logged:\n%s", logs)
	}

	if _, err := e.pool.Exec(t.Context(), `UPDATE admin_passkey SET credential = '"corrupt"'`); err != nil {
		t.Fatal(err)
	}
	if rec := c.postJSON("/admin/security/passkeys/begin", "{}"); rec.Code != 500 {
		t.Fatalf("add begin with unreadable passkeys: %d", rec.Code)
	}
	if logs := e.logs.String(); !strings.Contains(logs, "add passkey begin failed") || !strings.Contains(logs, "request_id="+testRequestID) {
		t.Fatalf("add begin failure not logged with request_id:\n%s", logs)
	}
}
