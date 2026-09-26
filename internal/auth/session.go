package auth

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"thymeandplace/internal/store"
)

const (
	CookieName      = "__Host-tp_admin"
	IdleTimeout     = 30 * time.Minute
	AbsoluteTimeout = 12 * time.Hour
	ReauthWindow    = 5 * time.Minute
	touchEvery      = time.Minute
)

type Session struct {
	TokenHash                     []byte
	CSRF, Method                  string
	CreatedAt, LastSeenAt, AuthAt time.Time
}

func (s Session) RecentAuth(now time.Time) bool { return now.Sub(s.AuthAt) <= ReauthWindow }

func (s *Service) Now() time.Time { return s.now() }

func (s *Service) CreateSession(ctx context.Context, method string) (string, Session, error) {
	raw, hash := NewToken()
	csrf, _ := NewToken()
	now := s.now()
	err := s.q().InsertSession(ctx, store.InsertSessionParams{TokenHash: hash, CsrfToken: csrf, Method: method, Now: ts(now)})
	return raw, Session{TokenHash: hash, CSRF: csrf, Method: method, CreatedAt: now, LastSeenAt: now, AuthAt: now}, err
}

func (s *Service) LookupSession(ctx context.Context, raw string) (Session, bool, error) {
	if raw == "" {
		return Session{}, false, nil
	}
	q := s.q()
	row, err := q.GetSession(ctx, HashToken(raw))
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, err
	}
	sess := Session{
		TokenHash: row.TokenHash, CSRF: row.CsrfToken, Method: row.Method,
		CreatedAt: row.CreatedAt.Time, LastSeenAt: row.LastSeenAt.Time, AuthAt: row.AuthAt.Time,
	}
	now := s.now()
	if now.Sub(sess.LastSeenAt) > IdleTimeout || now.Sub(sess.CreatedAt) > AbsoluteTimeout {
		_ = q.DeleteSession(ctx, sess.TokenHash)
		return Session{}, false, nil
	}
	if now.Sub(sess.LastSeenAt) > touchEvery {
		if err := q.TouchSession(ctx, store.TouchSessionParams{TokenHash: sess.TokenHash, Now: ts(now)}); err != nil {
			return Session{}, false, err
		}
		sess.LastSeenAt = now
	}
	return sess, true, nil
}

func (s *Service) MarkReauthed(ctx context.Context, sess Session) error {
	return s.q().SetSessionAuthAt(ctx, store.SetSessionAuthAtParams{TokenHash: sess.TokenHash, Now: ts(s.now())})
}

func (s *Service) DeleteSession(ctx context.Context, raw string) error {
	return s.q().DeleteSession(ctx, HashToken(raw))
}

func (s *Service) RevokeAllSessions(ctx context.Context) (int64, error) {
	return s.q().DeleteAllSessions(ctx)
}

func SetSessionCookie(w http.ResponseWriter, raw string) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: raw, Path: "/", MaxAge: int(AbsoluteTimeout.Seconds()),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
}

func ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
}
