// Package places searches cities and finds the nearest one to a point.
package places

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"thymeandplace/internal/store"
)

const (
	SearchLimit   = 8
	MaxQueryRunes = 100
	// MinQueryRunes is the fewest letters or digits a search needs: pg_trgm ignores every other character, and shorter queries match almost anything.
	MinQueryRunes = 2
	liveView      = "place"
)

type Place struct {
	ID         int32
	Name       string
	Admin1     string
	Country    string
	Population int32
	Lat, Lng   float64
	DistanceKm float64 // set by Nearest only
}

// Label is the display name: "Lisbon, PT" or "London, England, GB".
func (p Place) Label() string {
	if p.Admin1 != "" && p.Admin1 != p.Name {
		return p.Name + ", " + p.Admin1 + ", " + p.Country
	}
	return p.Name + ", " + p.Country
}

type Service struct{ db store.DBTX }

func NewService(db store.DBTX) *Service { return &Service{db: db} }

// Normalize drops invalid UTF-8 and control characters, trims, and cuts the
// query to MaxQueryRunes: the cleanup Search applies before querying.
func Normalize(q string) string {
	q = strings.ToValidUTF8(q, "")
	q = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, q)
	q = strings.TrimSpace(q)
	if utf8.RuneCountInString(q) > MaxQueryRunes {
		q = string([]rune(q)[:MaxQueryRunes])
	}
	return q
}

// TooShort reports whether q has fewer than MinQueryRunes letters or digits.
func TooShort(q string) bool {
	n := 0
	for _, r := range q {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			if n++; n >= MinQueryRunes {
				return false
			}
		}
	}
	return true
}

// Search returns up to SearchLimit places. Queries with fewer than
// MinQueryRunes letters or digits (including blank ones) return nil without
// touching the database; long queries are truncated to MaxQueryRunes.
func (s *Service) Search(ctx context.Context, q string) ([]Place, error) {
	q = Normalize(q)
	if TooShort(q) {
		return nil, nil
	}
	rows, err := store.SearchPlaces(ctx, s.db, liveView, q, SearchLimit)
	if err != nil {
		return nil, fmt.Errorf("place search: %w", err)
	}
	out := make([]Place, len(rows))
	for i, r := range rows {
		out[i] = fromRow(r)
	}
	return out, nil
}

func (s *Service) Nearest(ctx context.Context, lat, lng float64) (Place, bool, error) {
	r, ok, err := store.NearestPlace(ctx, s.db, liveView, lat, lng)
	if err != nil {
		return Place{}, false, fmt.Errorf("nearest place: %w", err)
	}
	return fromRow(r), ok, nil
}

func (s *Service) ByID(ctx context.Context, id int32) (Place, bool, error) {
	r, ok, err := store.PlaceByID(ctx, s.db, liveView, id)
	if err != nil {
		return Place{}, false, fmt.Errorf("place by id: %w", err)
	}
	return fromRow(r), ok, nil
}

func fromRow(r store.PlaceRow) Place {
	p := Place{ID: r.PlaceID, Name: r.Name, Country: r.Country, Population: r.Population,
		Lat: r.Lat, Lng: r.Lng, DistanceKm: r.DistanceM / 1000}
	if r.Admin1 != nil {
		p.Admin1 = *r.Admin1
	}
	return p
}
