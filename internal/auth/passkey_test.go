package auth

import (
	"bytes"
	"context"
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

func (d *device) register(t *testing.T, s *Service, name, token string) error {
	t.Helper()
	ctx := context.Background()
	opts, state, err := s.BeginRegistration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ao, err := virtualwebauthn.ParseAttestationOptions(string(opts))
	if err != nil {
		t.Fatal(err)
	}
	body := virtualwebauthn.CreateAttestationResponse(d.rp, d.auth, d.cred, *ao)
	err = s.FinishRegistration(ctx, state, httptest.NewRequest("POST", "/", bytes.NewBufferString(body)), name, token)
	if err == nil {
		d.auth.AddCredential(d.cred)
	}
	return err
}

func (d *device) login(t *testing.T, s *Service) error {
	t.Helper()
	ctx := context.Background()
	opts, state, err := s.BeginLogin(ctx)
	if err != nil {
		return err
	}
	ao, err := virtualwebauthn.ParseAssertionOptions(string(opts))
	if err != nil {
		t.Fatal(err)
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
	pks, _ := s.Passkeys(ctx)
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
	tok, _, _ := s.CreateEnrollmentToken(ctx)
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
	tok, _, _ := s.CreateEnrollmentToken(ctx)
	a := newDevice()
	_ = a.register(t, s, "Phone", tok)
	pks, _ := s.Passkeys(ctx)
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
	tok, _, _ := s.CreateEnrollmentToken(ctx)
	d := newDevice()
	_ = d.register(t, s, "Phone", tok)
	if err := s.FinishLogin(ctx, "not-a-sealed-state", httptest.NewRequest("POST", "/", bytes.NewBufferString("{}"))); !errors.Is(err, ErrCeremony) {
		t.Fatalf("tampered state: %v", err)
	}
}

// TestRemovePasskeyConcurrentRace guards against the racy count-then-delete
// pattern: two concurrent removals starting from exactly two passkeys must
// leave exactly one passkey, with exactly one call returning ErrLastPasskey
// (never both succeeding, and never both being rejected).
func TestRemovePasskeyConcurrentRace(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t, true)
	tokA, _, _ := s.CreateEnrollmentToken(ctx)
	a := newDevice()
	if err := a.register(t, s, "A", tokA); err != nil {
		t.Fatal(err)
	}
	b := newDevice()
	if err := b.register(t, s, "B", ""); err != nil {
		t.Fatal(err)
	}
	pks, _ := s.Passkeys(ctx)
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
// succeed, and the other must see ErrBadEnrollment.
func TestEnrollmentTokenConcurrentRegistrationRace(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t, true)
	tok, _, _ := s.CreateEnrollmentToken(ctx)

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
