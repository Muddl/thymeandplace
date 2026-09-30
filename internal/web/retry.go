package web

import (
	"net/url"
	"strconv"
	"strings"
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

// retryPath is the retry link for failures that happen before the request's
// parameters are validated (no snapshot yet, or a panic): the page's own
// path with no query, or the home page.
func retryPath(path string) string {
	switch {
	case path == "/season":
		return "/season"
	case strings.HasPrefix(path, "/ingredient/"):
		return "/ingredient/" + url.PathEscape(strings.TrimPrefix(path, "/ingredient/"))
	}
	return "/"
}
