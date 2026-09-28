package web

import (
	"encoding/xml"
	"strings"
	"testing"
)

var testFeatured = []Featured{{Name: "Lisbon, Portugal", Lat: 38.72, Lng: -9.14}, {Name: "Hobart, Australia", Lat: -42.8821, Lng: 147.3272}}

func discovery(d *Deps) {
	d.BaseURL = "https://example.test/" // the trailing slash is dropped
	d.Featured = testFeatured
}

func TestRobotsTxt(t *testing.T) {
	code, body, h := newHarness(t, discovery).get("/robots.txt", false)
	if code != 200 || !strings.HasPrefix(h.Get("Content-Type"), "text/plain") || h.Get("Cache-Control") == "" {
		t.Fatalf("robots.txt: %d %v", code, h)
	}
	for _, want := range []string{"User-agent: *\n", "Disallow: /admin\n", "Disallow: /places\n", "Disallow: /healthz\n", "Disallow: /readyz\n", "Sitemap: https://example.test/sitemap.xml\n"} {
		if !strings.Contains(body, want) {
			t.Errorf("robots.txt is missing %q:\n%s", want, body)
		}
	}
	// The content pages must stay crawlable.
	for _, p := range []string{"Disallow: /\n", "Disallow: /season", "Disallow: /ingredient", "Disallow: /static"} {
		if strings.Contains(body, p) {
			t.Errorf("robots.txt blocks content with %q", p)
		}
	}
}

// Every sitemap URL is absolute on the base URL and serves a 200 page.
func TestSitemapXML(t *testing.T) {
	hs := newHarness(t, discovery)
	code, _, h := hs.get("/sitemap.xml", false)
	if code != 200 || !strings.HasPrefix(h.Get("Content-Type"), "application/xml") {
		t.Fatalf("sitemap.xml: %d %v", code, h)
	}
	var set struct {
		XMLName xml.Name `xml:"http://www.sitemaps.org/schemas/sitemap/0.9 urlset"`
		URLs    []struct {
			Loc string `xml:"loc"`
		} `xml:"url"`
	}
	if err := xml.Unmarshal([]byte(rawBody(t, hs, "/sitemap.xml")), &set); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"https://example.test/", "https://example.test/about/data",
		"https://example.test/season?lat=38.7200&lng=-9.1400", "https://example.test/season?lat=-42.8821&lng=147.3272",
	}
	if len(set.URLs) != len(want) {
		t.Fatalf("sitemap has %d URLs, want %d: %+v", len(set.URLs), len(want), set.URLs)
	}
	for i, u := range set.URLs {
		if u.Loc != want[i] {
			t.Errorf("url %d = %q, want %q", i, u.Loc, want[i])
		}
		path := strings.TrimPrefix(u.Loc, "https://example.test")
		if code, _, _ := hs.get(path, false); code != 200 {
			t.Errorf("%s: %d", path, code)
		}
	}
}

func TestLLMsTxt(t *testing.T) {
	for _, tc := range []struct {
		name       string
		opts       []func(*Deps)
		want, gone []string
	}{
		{"seafood off", []func(*Deps){discovery, seafoodOff},
			[]string{"# Thyme & Place\n", "\n> ", "anywhere on land", "Tomato", "Basil", "[Hobart, Australia](https://example.test/season?lat=-42.8821&lng=147.3272)", "[About the data](https://example.test/about/data)"},
			[]string{"Mackerel", "Atlantic cod"}},
		{"seafood on", []func(*Deps){discovery},
			[]string{"seafood", "Mackerel", "Tomato"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body, h := newHarness(t, tc.opts...).get("/llms.txt", false)
			if code != 200 || !strings.HasPrefix(h.Get("Content-Type"), "text/plain") {
				t.Fatalf("llms.txt: %d %v", code, h)
			}
			for _, w := range tc.want {
				if !strings.Contains(body, w) {
					t.Errorf("llms.txt is missing %q:\n%s", w, body)
				}
			}
			for _, g := range tc.gone {
				if strings.Contains(body, g) {
					t.Errorf("llms.txt lists %q:\n%s", g, body)
				}
			}
		})
	}
}

// Without a configured base URL, the production origin is used.
func TestDiscoveryDefaultBaseURL(t *testing.T) {
	if _, body, _ := newHarness(t).get("/robots.txt", false); !strings.Contains(body, "Sitemap: "+DefaultBaseURL+"/sitemap.xml\n") {
		t.Errorf("robots.txt without a base URL:\n%s", body)
	}
}
