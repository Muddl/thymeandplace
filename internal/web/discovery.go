package web

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"thymeandplace/internal/web/views"
)

// DefaultBaseURL is the public origin when Deps.BaseURL is empty.
const DefaultBaseURL = "https://thymeand.place"

// Featured is a place the sitemap and llms.txt link a season page for.
type Featured struct {
	Name     string
	Lat, Lng float64
}

// seasonPath is the canonical season URL for a point, as the site links it.
func seasonPath(lat, lng float64) string {
	return fmt.Sprintf("/season?lat=%.4f&lng=%.4f", lat, lng)
}

func (s *Server) abs(path string) string { return s.d.BaseURL + path }

// discoveryCache is how long crawlers may keep robots.txt, sitemap.xml and
// llms.txt: they change only with a deploy or a catalog edit.
const discoveryCache = "public, max-age=3600"

// robots keeps crawlers off the admin, the search endpoint (a fragment with
// no content of its own) and the health checks. Season and ingredient pages
// stay crawlable: they are the site's content (§11.1).
func (s *Server) robots(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", discoveryCache)
	fmt.Fprintf(w, `User-agent: *
Disallow: /admin
Disallow: /places
Disallow: /healthz
Disallow: /readyz

Sitemap: %s
`, s.abs("/sitemap.xml"))
}

type sitemapURL struct {
	Loc string `xml:"loc"`
}

type urlset struct {
	XMLName xml.Name     `xml:"http://www.sitemaps.org/schemas/sitemap/0.9 urlset"`
	URLs    []sitemapURL `xml:"url"`
}

// sitemap lists the home and About pages, and a season page for each
// featured place. Ingredient pages are reached from season pages.
func (s *Server) sitemap(w http.ResponseWriter, _ *http.Request) {
	set := urlset{URLs: []sitemapURL{{s.abs("/")}, {s.abs("/about/data")}}}
	for _, p := range s.d.Featured {
		set.URLs = append(set.URLs, sitemapURL{s.abs(seasonPath(p.Lat, p.Lng))})
	}
	out, err := xml.MarshalIndent(set, "", "  ")
	if err != nil { // a fixed struct of strings always marshals
		http.Error(w, "sitemap unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", discoveryCache)
	_, _ = w.Write([]byte(xml.Header))
	_, _ = w.Write(out)
	_, _ = w.Write([]byte("\n"))
}

// llmsTxt is a Markdown summary of the site for language models
// (llmstxt.org): what it does, how far to trust it, and where to link.
// Like every public page, it doesn't mention seafood while that's off.
func (s *Server) llmsTxt(w http.ResponseWriter, _ *http.Request) {
	covers, where := "fruit, vegetables, herbs and mushrooms", "anywhere on land"
	if s.d.Seafood {
		covers, where = "fruit, vegetables, herbs, mushrooms and seafood", "on land and near the coast"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Thyme & Place\n\n> Thyme & Place estimates which %s are in season %s, month by month, with tips on choosing and storing each one and what it goes with.\n\n", covers, where)
	b.WriteString("Every result is an estimate, not a record of what a market sells. For a location, the site looks up 1991–2020 monthly climate normals for the 0.25° grid cell (about 28 km across) around it and applies simple rules per ingredient: growing temperatures, frost, months to harvest, winter chill and storage life. Estimates are checked against published seasonal guides for the places listed below. Local growers, greenhouses and imports all make a difference, so the site won't match every market.\n\n")
	fmt.Fprintf(&b, "%s\n\n", views.WildMushroomDisclaimer)

	b.WriteString("## Pages\n\n")
	fmt.Fprintf(&b, "- [Home](%s): search for a town or city, or use your location.\n", s.abs("/"))
	fmt.Fprintf(&b, "- [About the data](%s): how estimates work, data sources and licenses, privacy.\n\n", s.abs("/about/data"))

	b.WriteString("## Season pages\n\n")
	fmt.Fprintf(&b, "A season page lists what's in season this month near a point: `%s`. Each ingredient on it links to a page with its tips, pairings and a 12-month season chart for that place.\n\n", s.abs("/season?lat={latitude}&lng={longitude}"))
	for _, p := range s.d.Featured {
		fmt.Fprintf(&b, "- [%s](%s)\n", p.Name, s.abs(seasonPath(p.Lat, p.Lng)))
	}
	if len(s.d.Featured) > 0 {
		b.WriteString("\n")
	}

	if snap := s.d.Snapshot(); snap != nil {
		var names []string
		for _, ing := range snap.All() {
			if s.visible(ing) {
				names = append(names, ing.Name)
			}
		}
		slices.Sort(names)
		if len(names) > 0 {
			fmt.Fprintf(&b, "## Ingredients\n\n%s.\n", strings.Join(names, ", "))
		}
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", discoveryCache)
	_, _ = w.Write([]byte(b.String()))
}
