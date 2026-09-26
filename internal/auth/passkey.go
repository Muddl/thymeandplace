package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5/pgconn"

	"thymeandplace/internal/store"
)

const (
	StateCookie    = "__Host-tp_wa"
	EnrollmentTTL  = 15 * time.Minute
	maxPasskeyName = 60

	// removePasskeyLockKey is the pg_advisory_xact_lock key ("tp_pk" in
	// ASCII) that serializes RemovePasskey's count-then-delete so two
	// concurrent removals starting from two passkeys cannot both observe a
	// count of two and both proceed to delete, leaving zero.
	removePasskeyLockKey int64 = 0x74705f706b

	// uniqueViolation is the Postgres/SQLSTATE error code for a unique
	// constraint violation (used to detect a duplicate credential_id on
	// insert).
	uniqueViolation = "23505"
)

var adminUserID = []byte("thymeandplace-admin")

// ceremonyKind labels a sealed ceremony state so a state issued for one
// ceremony can never be finished by another: a login state never reaches
// AddPasskey or FinishEnrollment, a registration state never reaches
// FinishLogin, and an owner "add passkey" state never reaches
// FinishEnrollment (or vice versa). Without this, a registration SessionData
// (which carries a UserID and no AllowedCredentialIDs) can pass FinishLogin's
// checks even though it was never a login ceremony.
type ceremonyKind string

const (
	ceremonyLogin        ceremonyKind = "login"
	ceremonyRegistration ceremonyKind = "reg"    // signed-in owner adds a passkey
	ceremonyEnroll       ceremonyKind = "enroll" // enrollment link registers a passkey
)

// sealedState is the wire format sealed into the state cookie: the ceremony
// kind alongside the webauthn.SessionData it was issued for. For an
// "enroll" ceremony, TokenHash binds the state to the enrollment token that
// BeginEnrollment checked, so FinishEnrollment can only consume that token.
type sealedState struct {
	Kind      ceremonyKind         `json:"k"`
	SD        webauthn.SessionData `json:"sd"`
	TokenHash []byte               `json:"th,omitempty"`
}

type Passkey struct {
	ID         []byte
	Name       string
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

// adminUser adapts the single owner to webauthn.User.
type adminUser struct{ creds []webauthn.Credential }

func (u adminUser) WebAuthnID() []byte                         { return adminUserID }
func (u adminUser) WebAuthnName() string                       { return "admin" }
func (u adminUser) WebAuthnDisplayName() string                { return "Owner" }
func (u adminUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

func (s *Service) loadUser(ctx context.Context) (adminUser, []store.AdminPasskey, error) {
	rows, err := s.q().ListPasskeys(ctx)
	if err != nil {
		return adminUser{}, nil, err
	}
	u := adminUser{}
	for _, r := range rows {
		var c webauthn.Credential
		if err := json.Unmarshal(r.Credential, &c); err != nil {
			return adminUser{}, nil, fmt.Errorf("passkey %x: %w", r.CredentialID, err)
		}
		c.Authenticator.SignCount = uint32(r.SignCount)
		u.creds = append(u.creds, c)
	}
	return u, rows, nil
}

func (s *Service) Passkeys(ctx context.Context) ([]Passkey, error) {
	_, rows, err := s.loadUser(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Passkey, len(rows))
	for i, r := range rows {
		out[i] = Passkey{ID: r.CredentialID, Name: r.Name, CreatedAt: r.CreatedAt.Time}
		if r.LastUsedAt.Valid {
			t := r.LastUsedAt.Time
			out[i].LastUsedAt = &t
		}
	}
	return out, nil
}

func (s *Service) seal(kind ceremonyKind, sd *webauthn.SessionData, tokenHash []byte) (string, error) {
	b, err := json.Marshal(sealedState{Kind: kind, SD: *sd, TokenHash: tokenHash})
	if err != nil {
		return "", err
	}
	return b64u.EncodeToString(s.sealer.Seal(b)), nil
}

// unseal decrypts state, rejecting it unless it was sealed for the expected
// ceremony kind.
func (s *Service) unseal(kind ceremonyKind, state string) (sealedState, error) {
	var wrapped sealedState
	raw, err := b64u.DecodeString(state)
	if err != nil {
		return sealedState{}, err
	}
	plain, err := s.sealer.Open(raw)
	if err != nil {
		return sealedState{}, err
	}
	if err := json.Unmarshal(plain, &wrapped); err != nil {
		return sealedState{}, err
	}
	if wrapped.Kind != kind {
		return sealedState{}, fmt.Errorf("ceremony state is for %q, not %q", wrapped.Kind, kind)
	}
	return wrapped, nil
}

func (s *Service) BeginLogin(ctx context.Context) ([]byte, string, error) {
	u, _, err := s.loadUser(ctx)
	if err != nil {
		return nil, "", err
	}
	if len(u.creds) == 0 {
		return nil, "", ErrNoPasskeys
	}
	assertion, sd, err := s.wa.BeginLogin(u, webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return nil, "", err
	}
	opts, err := json.Marshal(assertion)
	if err != nil {
		return nil, "", err
	}
	state, err := s.seal(ceremonyLogin, sd, nil)
	return opts, state, err
}

func (s *Service) FinishLogin(ctx context.Context, state string, r *http.Request) error {
	st, err := s.unseal(ceremonyLogin, state)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCeremony, err)
	}
	u, _, err := s.loadUser(ctx)
	if err != nil {
		return err
	}
	cred, err := s.wa.FinishLogin(u, st.SD, r)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCeremony, err)
	}
	if cred.Authenticator.CloneWarning {
		return fmt.Errorf("%w: authenticator sign count went backwards (possible clone)", ErrCeremony)
	}
	b, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	return s.q().UpdatePasskeyUse(ctx, store.UpdatePasskeyUseParams{
		CredentialID: cred.ID, SignCount: int64(cred.Authenticator.SignCount), Credential: b, LastUsedAt: ts(s.now()),
	})
}

// beginCreation starts a WebAuthn registration ceremony for the owner and
// seals its state for kind, bound to tokenHash (enroll only).
func (s *Service) beginCreation(ctx context.Context, kind ceremonyKind, tokenHash []byte) ([]byte, string, error) {
	u, _, err := s.loadUser(ctx)
	if err != nil {
		return nil, "", err
	}
	var exclude []protocol.CredentialDescriptor
	for _, c := range u.creds {
		exclude = append(exclude, c.Descriptor())
	}
	creation, sd, err := s.wa.BeginRegistration(u, webauthn.WithExclusions(exclude))
	if err != nil {
		return nil, "", err
	}
	opts, err := json.Marshal(creation)
	if err != nil {
		return nil, "", err
	}
	state, err := s.seal(kind, sd, tokenHash)
	return opts, state, err
}

// BeginRegistration starts the signed-in owner's "add a passkey" ceremony.
// Its state can only be finished by AddPasskey. Callers must only reach it
// from behind a signed-in session with a recent re-auth.
func (s *Service) BeginRegistration(ctx context.Context) ([]byte, string, error) {
	return s.beginCreation(ctx, ceremonyRegistration, nil)
}

// AddPasskey finishes a BeginRegistration ceremony for the signed-in owner.
// It never accepts an enrollment ("enroll") or login state.
func (s *Service) AddPasskey(ctx context.Context, state string, r *http.Request, name string) error {
	st, err := s.unseal(ceremonyRegistration, state)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCeremony, err)
	}
	return s.finishCreation(ctx, st.SD, r, name, nil)
}

// BeginEnrollment starts a registration ceremony from an enrollment link.
// The token must currently be valid (ErrBadEnrollment otherwise); its hash is
// sealed into the returned state so FinishEnrollment can only complete this
// ceremony with this same token.
func (s *Service) BeginEnrollment(ctx context.Context, token string) ([]byte, string, error) {
	ok, err := s.EnrollmentValid(ctx, token)
	if err != nil {
		return nil, "", err
	}
	if !ok {
		return nil, "", ErrBadEnrollment
	}
	return s.beginCreation(ctx, ceremonyEnroll, HashToken(token))
}

// FinishEnrollment finishes a BeginEnrollment ceremony. token is required and
// must be the one the state was issued for (ErrBadEnrollment otherwise). The
// token is consumed in the same transaction as the passkey insert (via the
// conditional UPDATE in ConsumeEnrollmentToken), so two racing finishes
// sharing one token register at most one passkey, and a replayed state
// cannot register a second one.
func (s *Service) FinishEnrollment(ctx context.Context, state string, r *http.Request, name, token string) error {
	if token == "" {
		return ErrBadEnrollment
	}
	st, err := s.unseal(ceremonyEnroll, state)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCeremony, err)
	}
	hash := HashToken(token)
	if len(st.TokenHash) == 0 || subtle.ConstantTimeCompare(hash, st.TokenHash) != 1 {
		return ErrBadEnrollment
	}
	if ok, err := s.EnrollmentValid(ctx, token); err != nil {
		return err
	} else if !ok {
		return ErrBadEnrollment
	}
	return s.finishCreation(ctx, st.SD, r, name, hash)
}

// finishCreation verifies the new credential and stores it. A non-nil
// consumeHash names the enrollment token to consume in the same transaction
// as the insert; it is only ever non-nil from FinishEnrollment, which has
// already checked it against the sealed state.
func (s *Service) finishCreation(ctx context.Context, sd webauthn.SessionData, r *http.Request, name string, consumeHash []byte) error {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Passkey"
	}
	if len([]rune(name)) > maxPasskeyName {
		name = string([]rune(name)[:maxPasskeyName])
	}
	u, _, err := s.loadUser(ctx)
	if err != nil {
		return err
	}
	cred, err := s.wa.FinishRegistration(u, sd, r)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCeremony, err)
	}
	b, err := json.Marshal(cred)
	if err != nil {
		return err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := store.New(tx)
	now := s.now()
	if consumeHash != nil {
		n, err := q.ConsumeEnrollmentToken(ctx, store.ConsumeEnrollmentTokenParams{TokenHash: consumeHash, Now: ts(now)})
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrBadEnrollment
		}
	}
	if err := q.InsertPasskey(ctx, store.InsertPasskeyParams{
		CredentialID: cred.ID, PublicKey: cred.PublicKey, SignCount: int64(cred.Authenticator.SignCount),
		Credential: b, Name: name, CreatedAt: ts(now),
	}); err != nil {
		// The exclusion list beginCreation sends is only ever enforced
		// by the client/authenticator; go-webauthn's server-side
		// FinishRegistration does not re-check it against the response, so
		// a client that ignores it (or a replayed attestation) can reach
		// here with a credential_id that already exists. Map that to
		// ErrCeremony instead of leaking a raw pgconn unique-violation.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return fmt.Errorf("%w: credential already registered", ErrCeremony)
		}
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) RenamePasskey(ctx context.Context, id []byte, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > maxPasskeyName {
		return fmt.Errorf("name must be 1 to %d characters", maxPasskeyName)
	}
	n, err := s.q().RenamePasskey(ctx, store.RenamePasskeyParams{CredentialID: id, Name: name})
	if err == nil && n == 0 {
		return ErrPasskeyNotFound
	}
	return err
}

// RemovePasskey deletes a passkey, refusing to remove the last one. The
// count-then-delete runs inside a single transaction, serialized by a
// Postgres advisory lock held for the transaction's duration: without it,
// two concurrent removals starting from two passkeys could each see a count
// of two, and both proceed to delete, leaving zero passkeys and locking the
// owner out. The lock forces the second caller to wait until the first
// commits (or rolls back), at which point it recounts against the
// already-reduced total.
func (s *Service) RemovePasskey(ctx context.Context, id []byte) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", removePasskeyLockKey); err != nil {
		return err
	}
	q := store.New(tx)
	n, err := q.CountPasskeys(ctx)
	if err != nil {
		return err
	}
	if n <= 1 {
		return ErrLastPasskey
	}
	removed, err := q.DeletePasskey(ctx, id)
	if err != nil {
		return err
	}
	if removed == 0 {
		return ErrPasskeyNotFound
	}
	return tx.Commit(ctx)
}

func (s *Service) CreateEnrollmentToken(ctx context.Context) (string, time.Time, error) {
	raw, hash := NewToken()
	now := s.now()
	exp := now.Add(EnrollmentTTL)
	err := s.q().InsertEnrollmentToken(ctx, store.InsertEnrollmentTokenParams{TokenHash: hash, CreatedAt: ts(now), ExpiresAt: ts(exp)})
	return raw, exp, err
}

func (s *Service) EnrollmentValid(ctx context.Context, raw string) (bool, error) {
	if raw == "" {
		return false, nil
	}
	return s.q().EnrollmentTokenValid(ctx, store.EnrollmentTokenValidParams{TokenHash: HashToken(raw), Now: ts(s.now())})
}

func SetStateCookie(w http.ResponseWriter, state string) {
	http.SetCookie(w, &http.Cookie{Name: StateCookie, Value: state, Path: "/", MaxAge: 300,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
}

func ClearStateCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: StateCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
}
