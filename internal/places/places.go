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

// Search returns up to SearchLimit places. Blank queries return nil without
// touching the database; long queries are truncated to MaxQueryRunes.
func (s *Service) Search(ctx context.Context, q string) ([]Place, error) {
	q = strings.ToValidUTF8(q, "")
	q = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, q)
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(q) > MaxQueryRunes {
		q = string([]rune(q)[:MaxQueryRunes])
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
