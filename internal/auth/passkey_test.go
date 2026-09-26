package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/descope/virtualwebauthn"
)

type device struct {
	rp   virtualwebauthn.RelyingParty
	auth virtualwebauthn.Authenticator
	cred virtualwebauthn.Credential
}

func newDevice() *device {
	return &device{
		rp:   virtualwebauthn.RelyingParty{Name: "Thyme & Place", ID: "localhost", Origin: "https://localhost"},
		auth: virtualwebauthn.NewAuthenticator(),
		cred: virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2),
	}
}

// register drives a full registration ceremony against s. It returns any
// error from BeginRegistration, parsing the options, or FinishRegistration
// instead of calling t.Fatal, so it is safe to call from a goroutine (e.g.
// from a concurrency test) as well as from the main test body.
func (d *device) register(t *testing.T, s *Service, name, token string) error {
	t.Helper()
	ctx := context.Background()
	opts, state, err := s.BeginRegistration(ctx)
	if err != nil {
		return err
	}
	ao, err := virtualwebauthn.ParseAttestationOptions(string(opts))
	if err != nil {
		return err
	}
	body := virtualwebauthn.CreateAttestationResponse(d.rp, d.auth, d.cred, *ao)
	err = s.FinishRegistration(ctx, state, httptest.NewRequest("POST", "/", bytes.NewBufferString(body)), name, token)
	if err == nil {
		d.auth.AddCredential(d.cred)
	}
	return err
}

// login drives a full login ceremony against s. Like register, it returns
// errors rather than calling t.Fatal so it is goroutine-safe.
func (d *device) login(t *testing.T, s *Service) error {
	t.Helper()
	ctx := context.Background()
	opts, state, err := s.BeginLogin(ctx)
	if err != nil {
		return err
	}
	ao, err := virtualwebauthn.ParseAssertionOptions(string(opts))
	if err != nil {
		return err
	}
	body := virtualwebauthn.CreateAssertionResponse(d.rp, d.auth, d.cred, *ao)
	return s.FinishLogin(ctx, state, httptest.NewRequest("POST", "/", bytes.NewBufferString(body)))
}

func TestEnrollRegisterLogin(t *testing.T) {
	ctx := context.Background()
	s, c := newTestService(t, true)
	if _, _, err := s.BeginLogin(ctx); !errors.Is(err, ErrNoPasskeys) {
		t.Fatalf("no passkeys: %v", err)
	}
	tok, exp, err := s.CreateEnrollmentToken(ctx)
	if err != nil || !exp.Equal(c.now().Add(15*time.Minute)) {
		t.Fatalf("token: %v %v", exp, err)
	}
	phone := newDevice()
	if err := phone.register(t, s, "Phone", tok); err != nil {
		t.Fatalf("register: %v", err)
	}
	if ok, _ := s.EnrollmentValid(ctx, tok); ok {
		t.Fatal("token must be single-use")
	}
	if err := phone.login(t, s); err != nil {
		t.Fatalf("login: %v", err)
	}
	pks, err := s.Passkeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pks) != 1 || pks[0].Name != "Phone" || pks[0].LastUsedAt == nil {
		t.Fatalf("passkeys = %+v", pks)
	}

	stranger := newDevice()
	stranger.auth.AddCredential(stranger.cred)
	if err := stranger.login(t, s); !errors.Is(err, ErrCeremony) {
		t.Fatalf("unknown credential accepted: %v", err)
	}
}

func TestEnrollmentTokenExpiryAndReuse(t *testing.T) {
	ctx := context.Background()
	s, c := newTestService(t, true)
	tok, _, err := s.CreateEnrollmentToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c.add(15*time.Minute + time.Second)
	if ok, _ := s.EnrollmentValid(ctx, tok); ok {
		t.Fatal("expired token valid")
	}
	if err := newDevice().register(t, s, "Late", tok); !errors.Is(err, ErrBadEnrollment) {
		t.Fatalf("expired token registered: %v", err)
	}
}

func TestRenameRemoveAndLastPasskeyGuard(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t, true)
	tok, _, err := s.CreateEnrollmentToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a := newDevice()
	if err := a.register(t, s, "Phone", tok); err != nil {
		t.Fatal(err)
	}
	pks, err := s.Passkeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pks) != 1 {
		t.Fatalf("setup: want 1 passkey, got %d", len(pks))
	}
	if err := s.RemovePasskey(ctx, pks[0].ID); !errors.Is(err, ErrLastPasskey) {
		t.Fatalf("removed the only passkey: %v", err)
	}
	b := newDevice()
	if err := b.register(t, s, "Key", ""); err != nil { // signed-in add: no token
		t.Fatal(err)
	}
	if err := s.RenamePasskey(ctx, pks[0].ID, "Old phone"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemovePasskey(ctx, pks[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := a.login(t, s); !errors.Is(err, ErrCeremony) {
		t.Fatal("removed passkey still works")
	}
	if err := b.login(t, s); err != nil {
		t.Fatalf("remaining passkey: %v", err)
	}
}

func TestStateTampering(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t, true)
	tok, _, err := s.CreateEnrollmentToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	d := newDevice()
	if err := d.register(t, s, "Phone", tok); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishLogin(ctx, "not-a-sealed-state", httptest.NewRequest("POST", "/", bytes.NewBufferString("{}"))); !errors.Is(err, ErrCeremony) {
		t.Fatalf("tampered state: %v", err)
	}
}

// TestCeremonyStateBoundToKind guards against a sealed ceremony state being
// accepted by the wrong Finish* call. Before this state was tagged with its
// ceremony kind, a login SessionData and a registration SessionData had
// enough shared shape (both carry a Challenge and UserID; only registration
// lacks AllowedCredentialIDs, which some login paths treat permissively)
// that FinishLogin could accept a registration state, and nothing at all
// stopped a login state from reaching FinishRegistration.
func TestCeremonyStateBoundToKind(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t, true)
	tok, _, err := s.CreateEnrollmentToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	d := newDevice()
	if err := d.register(t, s, "Phone", tok); err != nil {
		t.Fatal(err)
	}

	t.Run("login state rejected by FinishRegistration", func(t *testing.T) {
		_, loginState, err := s.BeginLogin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		err = s.FinishRegistration(ctx, loginState, httptest.NewRequest("POST", "/", bytes.NewBufferString("{}")), "Impostor", "")
		if !errors.Is(err, ErrCeremony) {
			t.Fatalf("login state accepted by FinishRegistration: %v", err)
		}
	})

	t.Run("registration state rejected by FinishLogin with an otherwise-valid assertion", func(t *testing.T) {
		regOpts, regState, err := s.BeginRegistration(ctx)
		if err != nil {
			t.Fatal(err)
		}
		ao, err := virtualwebauthn.ParseAttestationOptions(string(regOpts))
		if err != nil {
			t.Fatal(err)
		}
		// A well-formed login assertion for the already-enrolled credential
		// "d", signed over the registration ceremony's own challenge -- what
		// an attacker controlling the client could produce by calling
		// navigator.credentials.get() with the registration challenge
		// instead of navigator.credentials.create(). Without the kind check,
		// this is exactly the shape the reported bug let through: the
		// unsealed registration SessionData has UserID set (matching the
		// owner) and no AllowedCredentialIDs to conflict with.
		assertionOpts := virtualwebauthn.AssertionOptions{Challenge: ao.Challenge, RelyingPartyID: d.rp.ID}
		body := virtualwebauthn.CreateAssertionResponse(d.rp, d.auth, d.cred, assertionOpts)
		err = s.FinishLogin(ctx, regState, httptest.NewRequest("POST", "/", bytes.NewBufferString(body)))
		if !errors.Is(err, ErrCeremony) {
			t.Fatalf("registration state accepted by FinishLogin: %v", err)
		}
	})
}

// TestFinishRegistrationDuplicateCredential exercises the pgconn
// unique-violation mapping: go-webauthn's FinishRegistration never re-checks
// the response's credential ID against the exclusion list BeginRegistration
// sent (that is enforced only by the client), and virtualwebauthn does not
// honor it either, so re-registering the same virtual credential produces a
// second, otherwise-valid attestation for a credential_id that already
// exists. The resulting primary-key violation must surface as ErrCeremony,
// not a raw pgconn error.
func TestFinishRegistrationDuplicateCredential(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t, true)
	tok, _, err := s.CreateEnrollmentToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	d := newDevice()
	if err := d.register(t, s, "Phone", tok); err != nil {
		t.Fatal(err)
	}
	if err := d.register(t, s, "Phone again", ""); !errors.Is(err, ErrCeremony) {
		t.Fatalf("duplicate credential: %v", err)
	}
}

// TestFinishRegistrationConsumedTokenRejected drives a real, full
// registration ceremony with a token that a prior full ceremony already
// consumed, rather than only asserting EnrollmentValid's return value.
func TestFinishRegistrationConsumedTokenRejected(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t, true)
	tok, _, err := s.CreateEnrollmentToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first := newDevice()
	if err := first.register(t, s, "First", tok); err != nil {
		t.Fatal(err)
	}
	second := newDevice()
	if err := second.register(t, s, "Second", tok); !errors.Is(err, ErrBadEnrollment) {
		t.Fatalf("consumed token accepted: %v", err)
	}
	pks, err := s.Passkeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pks) != 1 {
		t.Fatalf("want exactly 1 passkey registered, got %d", len(pks))
	}
}

// TestBeginRegistrationExcludesExistingCredential confirms the registration
// options actually list the owner's existing credential in
// excludeCredentials, which is what tells a browser not to offer to create
// a duplicate resident key on an authenticator that already holds it.
func TestBeginRegistrationExcludesExistingCredential(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t, true)
	tok, _, err := s.CreateEnrollmentToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	d := newDevice()
	if err := d.register(t, s, "Phone", tok); err != nil {
		t.Fatal(err)
	}

	opts, _, err := s.BeginRegistration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ao, err := virtualwebauthn.ParseAttestationOptions(string(opts))
	if err != nil {
		t.Fatal(err)
	}
	wantID := base64.RawURLEncoding.EncodeToString(d.cred.ID)
	found := false
	for _, id := range ao.ExcludeCredentials {
		if id == wantID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("excludeCredentials = %v, want to contain %s", ao.ExcludeCredentials, wantID)
	}
}

// TestRemovePasskeyConcurrentRace guards against the racy count-then-delete
// pattern: two concurrent removals starting from exactly two passkeys must
// leave exactly one passkey, with exactly one call returning ErrLastPasskey
// (never both succeeding, and never both being rejected).
func TestRemovePasskeyConcurrentRace(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t, true)
	tokA, _, err := s.CreateEnrollmentToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a := newDevice()
	if err := a.register(t, s, "A", tokA); err != nil {
		t.Fatal(err)
	}
	b := newDevice()
	if err := b.register(t, s, "B", ""); err != nil {
		t.Fatal(err)
	}
	pks, err := s.Passkeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pks) != 2 {
		t.Fatalf("setup: want 2 passkeys, got %d", len(pks))
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	ids := [][]byte{pks[0].ID, pks[1].ID}
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = s.RemovePasskey(ctx, ids[i])
		}(i)
	}
	wg.Wait()

	var okCount, lastCount int
	for _, err := range errs {
		switch {
		case err == nil:
			okCount++
		case errors.Is(err, ErrLastPasskey):
			lastCount++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if okCount != 1 || lastCount != 1 {
		t.Fatalf("want exactly one success and one ErrLastPasskey, got errs=%v", errs)
	}

	remaining, err := s.Passkeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 {
		t.Fatalf("want exactly 1 passkey left, got %d", len(remaining))
	}
}

// TestEnrollmentTokenConcurrentRegistrationRace guards against a token being
// consumed by two racing FinishRegistration calls: at most one should
// succeed, and the other must see ErrBadEnrollment. register() reports
// failures via its return value rather than t.Fatal, so it is safe to call
// from these goroutines.
func TestEnrollmentTokenConcurrentRegistrationRace(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t, true)
	tok, _, err := s.CreateEnrollmentToken(ctx)
	if err != nil {
		t.Fatal(err)
	}

	da := newDevice()
	db := newDevice()

	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		errs[0] = da.register(t, s, "A", tok)
	}()
	go func() {
		defer wg.Done()
		errs[1] = db.register(t, s, "B", tok)
	}()
	wg.Wait()

	var okCount, badCount int
	for _, err := range errs {
		switch {
		case err == nil:
			okCount++
		case errors.Is(err, ErrBadEnrollment):
			badCount++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if okCount != 1 || badCount != 1 {
		t.Fatalf("want exactly one success and one ErrBadEnrollment, got errs=%v", errs)
	}

	pks, err := s.Passkeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pks) != 1 {
		t.Fatalf("want exactly 1 passkey registered, got %d", len(pks))
	}
}
