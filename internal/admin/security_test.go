package admin

import (
	"encoding/base64"
	"net/url"
	"strings"
	"testing"
	"time"
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
