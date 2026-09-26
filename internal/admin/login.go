package admin

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"

	"thymeandplace/internal/admin/views"
	"thymeandplace/internal/auth"
	"thymeandplace/internal/web"
)

const maxCeremonyBody = 64 << 10

func (a *Admin) layout(r *http.Request, title string) views.LayoutVM {
	_, signedIn := SessionFrom(r.Context())
	return views.LayoutVM{Title: title, CSRF: CSRFToken(r.Context()), SignedIn: signedIn}
}

func (a *Admin) home(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, http.StatusOK, views.Home(a.layout(r, "Catalog")))
}

func (a *Admin) loginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := SessionFrom(r.Context()); ok {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	a.render(w, r, http.StatusOK, views.LoginPage(views.LoginVM{Layout: a.layout(r, "Sign in"), BackupEnabled: a.d.Auth.BackupEnabled()}))
}

// startSession issues a fresh session. If the request already carries a
// __Host-tp_admin cookie (e.g. a repeat login, such as backup login while a
// passkey session is still valid, or a stale/invalid leftover), that
// server-side session is deleted first so it can never be left alive
// alongside the new one; deleting an unknown or already-gone token is a
// harmless no-op.
func (a *Admin) startSession(w http.ResponseWriter, r *http.Request, method string) error {
	if c, err := r.Cookie(auth.CookieName); err == nil {
		if err := a.d.Auth.DeleteSession(r.Context(), c.Value); err != nil {
			a.d.Log.Error("startSession: replacing existing session failed", "err", err, "request_id", web.RequestIDFrom(r.Context()))
		}
	}
	raw, _, err := a.d.Auth.CreateSession(r.Context(), method)
	if err != nil {
		a.d.Log.Error("session create failed", "err", err, "method", method, "request_id", web.RequestIDFrom(r.Context()))
		return err
	}
	auth.SetSessionCookie(w, raw)
	return nil
}

func (a *Admin) passkeyBegin(w http.ResponseWriter, r *http.Request) {
	opts, state, err := a.d.Auth.BeginLogin(r.Context())
	if errors.Is(err, auth.ErrNoPasskeys) {
		jsonError(w, http.StatusConflict, "No passkeys are registered yet. Run admin enroll on the server.")
		return
	}
	if err != nil {
		a.d.Log.Error("passkey begin failed", "err", err)
		jsonError(w, http.StatusInternalServerError, "Couldn't start passkey sign-in.")
		return
	}
	auth.SetStateCookie(w, state)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(opts)
}

// limitCeremonyBody caps and pre-reads the WebAuthn JSON so a bad body is a
// 4xx, never a 500.
func limitCeremonyBody(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxCeremonyBody)
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	r.Body = io.NopCloser(bytes.NewReader(b))
	return nil
}

// finishPasskeyLogin runs the WebAuthn login-finish ceremony shared by every
// route that ends a passkey login: /admin/login/passkey/finish (fresh sign-in
// or a re-auth from an already-signed-in session) and
// /admin/security/reauth/finish (re-auth only). It applies the fail-closed
// per-IP rate limit, caps the ceremony body, clears the WebAuthn state cookie
// on every response (success or failure), and writes the outcome to
// admin_login_log — so every login attempt, from either route, is rate
// limited and audited exactly once. It reports whether the ceremony
// succeeded; on failure it has already written the error response and the
// caller must return without writing anything else.
func (a *Admin) finishPasskeyLogin(w http.ResponseWriter, r *http.Request) bool {
	ctx, ip := r.Context(), a.ip(r)
	state := ""
	if c, err := r.Cookie(auth.StateCookie); err == nil {
		state = c.Value
	}
	// Clear the WebAuthn state cookie on every response from this route,
	// success or failure, so a spent or abandoned ceremony state is never
	// left around to be replayed or to linger stale in the browser.
	auth.ClearStateCookie(w)
	if ok, err := a.d.Auth.AllowAttempt(ctx, ip); err != nil || !ok {
		if err != nil {
			a.d.Log.Error("passkey rate limit check failed", "err", err, "request_id", web.RequestIDFrom(ctx))
		}
		a.d.Auth.LogAttempt(ctx, ip, "passkey", "rate_limited")
		jsonError(w, http.StatusTooManyRequests, "Too many sign-in attempts. Wait 15 minutes and try again.")
		return false
	}
	err := limitCeremonyBody(w, r)
	if err == nil {
		err = a.d.Auth.FinishLogin(ctx, state, r)
	}
	if err != nil {
		a.d.Auth.LogAttempt(ctx, ip, "passkey", "failure")
		if !errors.Is(err, auth.ErrCeremony) {
			a.d.Log.Warn("passkey finish failed", "err", err)
		}
		jsonError(w, http.StatusUnauthorized, "That passkey didn't work. Try again, or use backup login.")
		return false
	}
	a.d.Auth.LogAttempt(ctx, ip, "passkey", "success")
	return true
}

func (a *Admin) passkeyFinish(w http.ResponseWriter, r *http.Request) {
	if !a.finishPasskeyLogin(w, r) {
		return
	}
	ctx := r.Context()
	if s, ok := SessionFrom(ctx); ok { // re-auth from an existing session
		if err := a.d.Auth.MarkReauthed(ctx, s); err != nil {
			a.d.Log.Error("mark reauthed failed", "err", err, "request_id", web.RequestIDFrom(ctx))
			jsonError(w, http.StatusInternalServerError, "Signed in, but couldn't refresh your session. Try again.")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"redirect": "/admin"})
		return
	}
	if err := a.startSession(w, r, "passkey"); err != nil {
		jsonError(w, http.StatusInternalServerError, "Signed in, but couldn't start a session.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"redirect": "/admin"})
}

func (a *Admin) backupPage(w http.ResponseWriter, r *http.Request) {
	if !a.d.Auth.BackupEnabled() {
		http.NotFound(w, r)
		return
	}
	a.render(w, r, http.StatusOK, views.BackupPage(views.BackupVM{Layout: a.layout(r, "Backup login")}))
}

func (a *Admin) backupSubmit(w http.ResponseWriter, r *http.Request) {
	ctx, ip := r.Context(), a.ip(r)
	// A stray WebAuthn state cookie (from an abandoned passkey attempt) must
	// not linger past a backup login response either.
	auth.ClearStateCookie(w)
	if !a.d.Auth.BackupEnabled() {
		a.d.Auth.LogAttempt(ctx, ip, "backup", "disabled")
		http.NotFound(w, r)
		return
	}
	page := func(status int, msg string) {
		a.render(w, r, status, views.BackupPage(views.BackupVM{Layout: a.layout(r, "Backup login"), Message: msg}))
	}
	if ok, err := a.d.Auth.AllowAttempt(ctx, ip); err != nil || !ok {
		if err != nil {
			a.d.Log.Error("backup rate limit check failed", "err", err, "request_id", web.RequestIDFrom(ctx))
		}
		a.d.Auth.LogAttempt(ctx, ip, "backup", "rate_limited")
		page(http.StatusTooManyRequests, "Too many sign-in attempts. Wait 15 minutes and try again.")
		return
	}
	err := a.d.Auth.BackupLogin(ctx, r.PostFormValue("password"), r.PostFormValue("code"))
	var locked *auth.LockedError
	switch {
	case err == nil:
		a.d.Auth.LogAttempt(ctx, ip, "backup", "success")
		if err := a.startSession(w, r, "backup"); err != nil {
			page(http.StatusInternalServerError, "Signed in, but couldn't start a session.")
			return
		}
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
	case errors.As(err, &locked):
		a.d.Auth.LogAttempt(ctx, ip, "backup", "locked")
		page(http.StatusForbidden, fmt.Sprintf("Backup login is locked until %s UTC. Passkey sign-in still works.", locked.Until.UTC().Format("15:04")))
	case errors.Is(err, auth.ErrBackupNotSet):
		a.d.Auth.LogAttempt(ctx, ip, "backup", "failure")
		page(http.StatusUnauthorized, "Backup login isn't set up. Run admin set-backup on the server.")
	case errors.Is(err, auth.ErrBadCredentials):
		a.d.Auth.LogAttempt(ctx, ip, "backup", "failure")
		page(http.StatusUnauthorized, "Password or code is wrong.")
	default:
		a.d.Auth.LogAttempt(ctx, ip, "backup", "error")
		a.d.Log.Error("backup login error", "err", err, "request_id", web.RequestIDFrom(ctx))
		page(http.StatusInternalServerError, "Something went wrong. Try again.")
	}
}

func (a *Admin) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(auth.CookieName); err == nil {
		if err := a.d.Auth.DeleteSession(r.Context(), c.Value); err != nil {
			a.d.Log.Error("logout: session delete failed", "err", err, "request_id", web.RequestIDFrom(r.Context()))
		}
	}
	auth.ClearSessionCookie(w)
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

func (a *Admin) enrollPage(w http.ResponseWriter, r *http.Request) {
	tok := r.URL.Query().Get("token")
	ok, err := a.d.Auth.EnrollmentValid(r.Context(), tok)
	if err != nil {
		// A database error says nothing about the link; don't tell the
		// owner it is invalid or expired.
		a.d.Log.Error("enrollment check failed", "err", err, "request_id", web.RequestIDFrom(r.Context()))
		http.Error(w, "Something went wrong. Try again.", http.StatusServiceUnavailable)
		return
	}
	status := http.StatusOK
	if !ok {
		status = http.StatusBadRequest
	}
	a.render(w, r, status, views.EnrollPage(views.EnrollVM{Layout: a.layout(r, "Register a passkey"), Token: tok, Valid: ok}))
}

func (a *Admin) enrollBegin(w http.ResponseWriter, r *http.Request) {
	opts, state, err := a.d.Auth.BeginEnrollment(r.Context(), r.URL.Query().Get("token"))
	if errors.Is(err, auth.ErrBadEnrollment) {
		jsonError(w, http.StatusBadRequest, "This enrollment link is invalid or has expired. Run admin enroll again.")
		return
	}
	if err != nil {
		a.d.Log.Error("enrollment begin failed", "err", err, "request_id", web.RequestIDFrom(r.Context()))
		jsonError(w, http.StatusServiceUnavailable, "Couldn't start passkey registration. Try again.")
		return
	}
	auth.SetStateCookie(w, state)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(opts)
}

func (a *Admin) enrollFinish(w http.ResponseWriter, r *http.Request) {
	ctx, ip := r.Context(), a.ip(r)
	state := ""
	if c, err := r.Cookie(auth.StateCookie); err == nil {
		state = c.Value
	}
	// See passkeyFinish: the state cookie is cleared on every response here too.
	auth.ClearStateCookie(w)
	err := limitCeremonyBody(w, r)
	if err == nil {
		err = a.d.Auth.FinishEnrollment(ctx, state, r, r.URL.Query().Get("name"), r.URL.Query().Get("token"))
	}
	switch {
	case errors.Is(err, auth.ErrBadEnrollment):
		a.d.Auth.LogAttempt(ctx, ip, "enroll", "failure")
		jsonError(w, http.StatusBadRequest, "This enrollment link is invalid or has expired. Run admin enroll again.")
		return
	case err != nil:
		a.d.Auth.LogAttempt(ctx, ip, "enroll", "failure")
		if !errors.Is(err, auth.ErrCeremony) {
			a.d.Log.Error("enrollment finish failed", "err", err, "request_id", web.RequestIDFrom(ctx))
		}
		jsonError(w, http.StatusBadRequest, "The passkey couldn't be registered. Try again.")
		return
	}
	a.d.Auth.LogAttempt(ctx, ip, "enroll", "success")
	if err := a.startSession(w, r, "enroll"); err != nil {
		jsonError(w, http.StatusInternalServerError, "Registered, but couldn't start a session. Sign in.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"redirect": "/admin"})
}
