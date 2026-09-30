package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"thymeandplace/internal/climate"
)

// seasonRetryURL rebuilds a /season URL for an error panel's retry link from
// validated values only: the parsed coordinates (exact, so the retry asks for
// the same cell), the place ID if it is a valid int32, and a date that
// validDate already accepted. Nothing from the raw query is echoed.
func seasonRetryURL(lat, lng float64, placeParam, date string) string {
	u := "/season?lat=" + strconv.FormatFloat(lat, 'f', -1, 64) + "&lng=" + strconv.FormatFloat(lng, 'f', -1, 64)
	if id, err := strconv.ParseInt(placeParam, 10, 32); err == nil {
		u += "&place=" + strconv.FormatInt(id, 10)
	}
	if date != "" {
		u += "&date=" + url.QueryEscape(date)
	}
	return u
}

// retryURL is the retry link for failures that happen before the handler has
// validated the request (no snapshot yet, or a panic). It validates the query
// the way the handler would and rebuilds the page URL from the parsed values,
// so the retry reloads the same page; if that fails, it falls back to
// retryPath.
func (s *Server) retryURL(r *http.Request) string {
	path, q := r.URL.Path, r.URL.Query()
	switch {
	case path == "/season":
		if lat, lng, ok := parseLatLng(q.Get("lat"), q.Get("lng")); ok {
			return seasonRetryURL(lat, lng, q.Get("place"), s.validDate(q.Get("date")))
		}
	case strings.HasPrefix(path, "/ingredient/"):
		if cell, err := strconv.Atoi(q.Get("cell")); err == nil && climate.ValidCellID(cell) {
			return ingredientURL(strings.TrimPrefix(path, "/ingredient/"), cell, s.validDate(q.Get("date")))
		}
	}
	return retryPath(path)
}

// retryPath is the fallback retry link when the query can't be validated:
// the page's own path with no query, or the home page.
func retryPath(path string) string {
	switch {
	case path == "/season":
		return "/season"
	case strings.HasPrefix(path, "/ingredient/"):
		return "/ingredient/" + url.PathEscape(strings.TrimPrefix(path, "/ingredient/"))
	}
	return "/"
}
