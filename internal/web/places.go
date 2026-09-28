package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"thymeandplace/internal/places"
	"thymeandplace/internal/web/views"
)

func seasonURL(lat, lng float64, placeID int32) string {
	return fmt.Sprintf("/season?lat=%.4f&lng=%.4f&place=%d", lat, lng, placeID)
}

// maxEchoRunes caps how much of the query the no-match message echoes back.
const maxEchoRunes = 100

func truncateRunes(s string, n int) string {
	i := 0
	for j := range s {
		if i == n {
			return s[:j]
		}
		i++
	}
	return s
}

func (s *Server) places(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		s.render(w, r, http.StatusOK, "Search", "", views.PlaceList(nil), nil)
		return
	}
	var found []places.Place
	err := s.withDB(r.Context(), func(ctx context.Context) error {
		var err error
		found, err = s.d.Places.Search(ctx, q)
		return err
	})
	if errors.Is(err, errBusy) {
		w.Header().Set("Retry-After", shedRetryAfter)
		s.render(w, r, http.StatusServiceUnavailable, "Search", q, views.PlacesUnavailable(), nil)
		return
	}
	if err != nil {
		s.d.Log.Warn("place search failed", "err", err, "request_id", RequestIDFrom(r.Context()))
		s.render(w, r, http.StatusServiceUnavailable, "Search", q, views.PlacesUnavailable(), nil)
		return
	}
	if len(found) == 0 {
		s.render(w, r, http.StatusOK, "Search", q, views.PlacesNoMatch(truncateRunes(q, maxEchoRunes)), nil)
		return
	}
	items := make([]views.PlaceItem, len(found))
	for i, p := range found {
		items[i] = views.PlaceItem{Label: p.Label(), URL: seasonURL(p.Lat, p.Lng, p.ID)}
	}
	s.render(w, r, http.StatusOK, "Search", q, views.PlaceList(items), nil)
}
