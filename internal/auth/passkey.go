package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

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
)

var adminUserID = []byte("thymeandplace-admin")

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

func (s *Service) seal(sd *webauthn.SessionData) (string, error) {
	b, err := json.Marshal(sd)
	if err != nil {
		return "", err
	}
	return b64u.EncodeToString(s.sealer.Seal(b)), nil
}

func (s *Service) unseal(state string) (webauthn.SessionData, error) {
	var sd webauthn.SessionData
	raw, err := b64u.DecodeString(state)
	if err != nil {
		return sd, err
	}
	plain, err := s.sealer.Open(raw)
	if err != nil {
		return sd, err
	}
	return sd, json.Unmarshal(plain, &sd)
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
	state, err := s.seal(sd)
	return opts, state, err
}

func (s *Service) FinishLogin(ctx context.Context, state string, r *http.Request) error {
	sd, err := s.unseal(state)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCeremony, err)
	}
	u, _, err := s.loadUser(ctx)
	if err != nil {
		return err
	}
	cred, err := s.wa.FinishLogin(u, sd, r)
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

func (s *Service) BeginRegistration(ctx context.Context) ([]byte, string, error) {
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
	state, err := s.seal(sd)
	return opts, state, err
}

// FinishRegistration verifies the new credential and stores it. With an
// enrollment token, the token is consumed in the same transaction as the
// passkey insert (via the conditional UPDATE in ConsumeEnrollmentToken), so
// two racing finishes sharing one token register at most one passkey.
func (s *Service) FinishRegistration(ctx context.Context, state string, r *http.Request, name, enrollToken string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Passkey"
	}
	if len([]rune(name)) > maxPasskeyName {
		name = string([]rune(name)[:maxPasskeyName])
	}
	if enrollToken != "" {
		if ok, err := s.EnrollmentValid(ctx, enrollToken); err != nil {
			return err
		} else if !ok {
			return ErrBadEnrollment
		}
	}
	sd, err := s.unseal(state)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCeremony, err)
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
	if enrollToken != "" {
		n, err := q.ConsumeEnrollmentToken(ctx, store.ConsumeEnrollmentTokenParams{TokenHash: HashToken(enrollToken), Now: ts(now)})
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
		return errors.New("passkey not found")
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
		return errors.New("passkey not found")
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
