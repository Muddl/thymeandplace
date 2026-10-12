package web

import (
	"strings"
	"testing"
)

func TestThemeSwitch(t *testing.T) {
	hs := newHarness(t)

	_, body, h := hs.get("/", false)
	if !strings.Contains(body, "/static/app.css") || strings.Contains(body, "design/") || h.Get("Set-Cookie") != "" {
		t.Errorf("no theme should serve the shipped stylesheet only:\n%s", body)
	}

	for _, v := range []string{"a", "b", "c"} {
		_, body, h := hs.get("/?theme="+v, false)
		for _, want := range []string{"design/tokens-" + v + ".css", "design/base.css", "design/variant-" + v + ".css"} {
			if !strings.Contains(body, want) {
				t.Errorf("theme %s: missing %s", v, want)
			}
		}
		if strings.Contains(body, "/static/app.css") {
			t.Errorf("theme %s still links app.css", v)
		}
		if !strings.Contains(h.Get("Set-Cookie"), themeCookie+"="+v) {
			t.Errorf("theme %s: cookie not set: %q", v, h.Get("Set-Cookie"))
		}
		if strings.Contains(body, "<style") || strings.Contains(body, " style=") {
			t.Errorf("themed page has inline style, which the CSP blocks")
		}
	}

	_, body, _ = hs.get("/", false, "Cookie", themeCookie+"=b")
	if !strings.Contains(body, "design/variant-b.css") {
		t.Error("the cookie should keep the theme on later pages")
	}
	_, body, _ = hs.get("/?theme=off", false, "Cookie", themeCookie+"=b")
	if strings.Contains(body, "design/") {
		t.Error("?theme=off should return to the shipped stylesheet")
	}
	_, body, _ = hs.get("/?theme=../x", false)
	if strings.Contains(body, "design/") {
		t.Error("an unknown theme must be ignored")
	}
	_, body, _ = hs.get("/", true, "Cookie", themeCookie+"=b")
	if strings.Contains(body, "<link") {
		t.Error("htmx partials carry no stylesheet links")
	}
}
