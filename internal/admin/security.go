package admin

import (
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"

	"thymeandplace/internal/admin/views"
	"thymeandplace/internal/auth"
	"thymeandplace/internal/web"
)

func (a *Admin) registerSecurity() {
	m := a.mux
	m.Handle("GET /admin/security", a.requireAuth(http.HandlerFunc(a.securityPage)))
	m.Handle("POST /admin/security/reauth/begin", a.requireAuth(http.HandlerFunc(a.passkeyBegin)))
	m.Handle("POST /admin/security/reauth/finish", a.requireAuth(http.HandlerFunc(a.reauthFinish)))
	m.Handle("POST /admin/security/passkeys/begin", a.requireAuth(a.requireRecent(http.HandlerFunc(a.addBegin))))
	m.Handle("POST /admin/security/passkeys/finish", a.requireAuth(a.requireRecent(http.HandlerFunc(a.addFinish))))
	m.Handle("POST /admin/security/passkeys/{id}/rename", a.requireAuth(http.HandlerFunc(a.renamePasskey)))
	m.Handle("POST /admin/security/passkeys/{id}/remove", a.requireAuth(http.HandlerFunc(a.removePasskey)))
}

func (a *Admin) recent(r *http.Request) bool {
	s, ok := SessionFrom(r.Context())
	return ok && s.RecentAuth(a.d.Auth.Now())
}

func (a *Admin) requireRecent(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.recent(r) {
			jsonError(w, http.StatusForbidden, "Confirm it's you first: sign in with a passkey again.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *Admin) securityVM(r *http.Request, msg string) (views.SecurityVM, error) {
	pks, err := a.d.Auth.Passkeys(r.Context())
	if err != nil {
		return views.SecurityVM{}, err
	}
	vm := views.SecurityVM{Layout: a.layout(r, "Security"), RecentAuth: a.recent(r), BackupEnabled: a.d.Auth.BackupEnabled(), Message: msg}
	for _, p := range pks {
		row := views.PasskeyRow{ID: base64.RawURLEncoding.EncodeToString(p.ID), Name: p.Name, Created: p.CreatedAt.UTC().Format("2006-01-02"), LastUsed: "never"}
		if p.LastUsedAt != nil {
			row.LastUsed = p.LastUsedAt.UTC().Format("2006-01-02 15:04 UTC")
		}
		vm.Passkeys = append(vm.Passkeys, row)
	}
	return vm, nil
}

func (a *Admin) securityPage(w http.ResponseWriter, r *http.Request) {
	a.securityWithMessage(w, r, http.StatusOK, "")
}

func (a *Admin) securityWithMessage(w http.ResponseWriter, r *http.Request, status int, msg string) {
	vm, err := a.securityVM(r, msg)
	if err != nil {
		a.d.Log.Error("security page", "err", err)
		http.Error(w, "Couldn't load passkeys.", http.StatusInternalServerError)
		return
	}
	a.render(w, r, status, views.SecurityPage(vm))
}

// reauthFinish confirms a fresh passkey sign-in for an already-signed-in
// session, going through the same rate limiting, ceremony body cap, state
// cookie handling, and admin_login_log audit trail as any other passkey
// login finish (see finishPasskeyLogin). requireAuth guarantees a session is
// present here.
func (a *Admin) reauthFinish(w http.ResponseWriter, r *http.Request) {
	if !a.finishPasskeyLogin(w, r) {
		return
	}
	ctx := r.Context()
	s, _ := SessionFrom(ctx)
	if err := a.d.Auth.MarkReauthed(ctx, s); err != nil {
		a.d.Log.Error("mark reauthed failed", "err", err, "request_id", web.RequestIDFrom(ctx))
		jsonError(w, http.StatusInternalServerError, "Couldn't record the confirmation.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"redirect": "/admin/security"})
}

func (a *Admin) addBegin(w http.ResponseWriter, r *http.Request) {
	opts, state, err := a.d.Auth.BeginRegistration(r.Context())
	if err != nil {
		a.d.Log.Error("add passkey begin failed", "err", err, "request_id", web.RequestIDFrom(r.Context()))
		jsonError(w, http.StatusInternalServerError, "Couldn't start passkey registration.")
		return
	}
	auth.SetStateCookie(w, state)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(opts)
}

func (a *Admin) addFinish(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	state := ""
	if c, err := r.Cookie(auth.StateCookie); err == nil {
		state = c.Value
	}
	// As on every finish route, clear the state cookie on every response.
	auth.ClearStateCookie(w)
	err := limitCeremonyBody(w, r)
	if err == nil {
		err = a.d.Auth.AddPasskey(ctx, state, r, r.URL.Query().Get("name"))
	}
	if err != nil {
		lvl := slog.LevelError
		if errors.Is(err, auth.ErrCeremony) {
			lvl = slog.LevelWarn
		}
		a.d.Log.Log(ctx, lvl, "add passkey finish failed", "err", err, "request_id", web.RequestIDFrom(ctx))
		jsonError(w, http.StatusBadRequest, "The passkey couldn't be registered. Try again.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"redirect": "/admin/security"})
}

func passkeyID(r *http.Request) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(r.PathValue("id"))
}

// unknownPasskeyMessage is the only text ever shown for a bad passkey ID,
// whether the ID failed to decode or decoded fine but matches nothing: never
// echo the raw decode or database error into the page.
const unknownPasskeyMessage = "Unknown passkey."

func (a *Admin) renamePasskey(w http.ResponseWriter, r *http.Request) {
	id, err := passkeyID(r)
	if err != nil {
		a.securityWithMessage(w, r, http.StatusBadRequest, unknownPasskeyMessage)
		return
	}
	switch err = a.d.Auth.RenamePasskey(r.Context(), id, r.PostFormValue("name")); {
	case errors.Is(err, auth.ErrPasskeyNotFound):
		a.securityWithMessage(w, r, http.StatusNotFound, unknownPasskeyMessage)
	case err != nil:
		a.securityWithMessage(w, r, http.StatusBadRequest, "Couldn't rename the passkey.")
	default:
		http.Redirect(w, r, "/admin/security", http.StatusSeeOther)
	}
}

func (a *Admin) removePasskey(w http.ResponseWriter, r *http.Request) {
	if !a.recent(r) {
		a.securityWithMessage(w, r, http.StatusForbidden, "Confirm it's you before removing a passkey.")
		return
	}
	id, err := passkeyID(r)
	if err != nil {
		a.securityWithMessage(w, r, http.StatusBadRequest, unknownPasskeyMessage)
		return
	}
	switch err = a.d.Auth.RemovePasskey(r.Context(), id); {
	case errors.Is(err, auth.ErrLastPasskey):
		a.securityWithMessage(w, r, http.StatusConflict, "Add another passkey before removing this one.")
	case errors.Is(err, auth.ErrPasskeyNotFound):
		a.securityWithMessage(w, r, http.StatusNotFound, unknownPasskeyMessage)
	case err != nil:
		a.securityWithMessage(w, r, http.StatusBadRequest, "Couldn't remove the passkey.")
	default:
		http.Redirect(w, r, "/admin/security", http.StatusSeeOther)
	}
}
