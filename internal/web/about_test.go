package web

import (
	"html"
	"regexp"
	"strings"
	"testing"

	"thymeandplace/internal/ingest"
)

var tagRE = regexp.MustCompile(`<[^>]*>`)

// pageText is a page's visible text: tags stripped, entities unescaped,
// whitespace collapsed.
func pageText(t *testing.T, hs *harness, path string) string {
	t.Helper()
	return strings.Join(strings.Fields(html.UnescapeString(tagRE.ReplaceAllString(rawBody(t, hs, path), ""))), " ")
}

// The About page must carry every credit line each dataset in use declares,
// and make no licensing claim that no recorded review supports. With seafood
// off, the ocean data isn't used, so its credits aren't shown.
func TestAboutCarriesDatasetAttributions(t *testing.T) {
	for _, tc := range []struct {
		name  string
		opts  []func(*Deps)
		shown []ingest.Dataset
		gone  []ingest.Dataset
	}{
		{name: "seafood on", shown: []ingest.Dataset{ingest.NewPlaces(), ingest.NewOcean(""), ingest.NewClimate("")}},
		{name: "seafood off", opts: []func(*Deps){seafoodOff},
			shown: []ingest.Dataset{ingest.NewPlaces(), ingest.NewClimate("")}, gone: []ingest.Dataset{ingest.NewOcean("")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := pageText(t, newHarness(t, tc.opts...), "/about/data")
			for _, ds := range tc.shown {
				lines := ds.Attribution()
				if len(lines) == 0 {
					t.Errorf("%s declares no attribution", ds.Name())
				}
				for _, line := range lines {
					if !strings.Contains(text, line) {
						t.Errorf("/about/data is missing %s attribution %q:\n%s", ds.Name(), line, text)
					}
				}
			}
			for _, ds := range tc.gone {
				for _, line := range ds.Attribution() {
					if strings.Contains(text, line) {
						t.Errorf("/about/data credits unused dataset %s: %q", ds.Name(), line)
					}
				}
			}
			if strings.Contains(text, "used under its data licence") {
				t.Error("/about/data still claims a MEOW data licence that nothing records")
			}
		})
	}
}
