// Package views holds the templ components for the public site.
package views

import (
	"time"

	"github.com/a-h/templ"

	"thymeandplace/internal/season"
	"thymeandplace/static"
)

// User-facing copy (spec §11.6). Tests assert these exact strings.
const (
	CopyNoMatchSuffix      = "Try a nearby larger town or city."
	CopyGeoOff             = "Location access is off, so search for your town instead."
	CopyUnsupported        = "We can't estimate seasons here yet. Estimates cover land and coastal waters."
	CopyUnsupportedLand    = "We can't estimate seasons here yet. Estimates cover land areas only." // seafood off
	CopyNotLocal           = "Not grown or landed near here, as far as our estimates can tell."
	CopyNotGrown           = "Not grown near here, as far as our estimates can tell." // seafood off
	CopySearchAnother      = "Search another place"
	CopyHarsh              = "Not much grows locally this time of year. Here's what's still around."
	CopyLoadError          = "Couldn't load seasonal picks right now. Try again in a moment."
	CopyTryAgain           = "Try again"
	CopyPaused             = "Search is pausing for a few seconds. Keep typing and results will catch up."
	CopySearchUnavailable  = "Search isn't available right now. Try again in a moment."
	CopyNotFound           = "We couldn't find that page."
	CopyKeepTyping         = "Keep typing: enter at least 2 letters of your town's name."
	WildMushroomDisclaimer = "Look for wild mushrooms at markets and from sellers who can identify them. We don't give foraging advice: many edible mushrooms have deadly look-alikes."
	CopyLandEstimate       = "Estimated from local climate normals."
)

// HTMX config: swap 4xx/5xx so error partials render (§11.5), and keep no
// history snapshots in localStorage (they would hold place labels); history
// restores refetch the full page instead. Eval (trigger filters, hx-on, js:
// values) and script tags in swapped content are off, and requests stay on
// this origin; the CSP would block the first two anyway.
const htmxConfig = `{"responseHandling":[{"code":"204","swap":false},{"code":"[23]..","swap":true},{"code":"[45]..","swap":true,"error":true}],"includeIndicatorStyles":false,"historyCacheSize":0,"allowEval":false,"allowScriptTags":false,"selfRequestsOnly":true}`

// estimatesIntro opens "How estimates work". Only land results are estimates
// when seafood (hand-curated months) is shown too.
func estimatesIntro(seafood bool) string {
	lead := "Every result is an estimate."
	if seafood {
		lead = "Every land result is an estimate."
	}
	return lead + " We look up 1991–2020 monthly climate normals for the 0.25° grid cell (about 28 km across) around your location, then apply simple rules per ingredient:"
}

// SiteDescription is the meta description for pages without their own.
func SiteDescription(seafood bool) string {
	covers := "fruit, vegetables, herbs and mushrooms"
	if seafood {
		covers = "fruit, vegetables, herbs, mushrooms and seafood"
	}
	return "Find out which " + covers + " are in season where you live, with tips on choosing, storing and pairing them."
}

const AboutDescription = "How Thyme & Place estimates what's in season: climate data, simple rules per ingredient, sources, licenses and privacy."

func SeasonDescription(label, month string) string {
	return "What's in season near " + label + " in " + month + ", estimated from the local climate, with tips on choosing, storing and pairing each ingredient."
}

func IngredientDescription(name string) string {
	return name + ": when it's in season near you, how to choose and store it, and what it goes with."
}

// Asset is the versioned URL of an embedded static file (static.Asset).
func Asset(name string) string { return static.Asset(name) }

type PageVM struct {
	Title       string
	Description string // the meta description search results show
	Query       string
	Places      templ.Component
	Results     templ.Component
}

type PlaceItem struct{ Label, URL string }

type SeasonVM struct {
	Label     string
	MonthName string
	Harsh     bool
	Groups    []Group
}

type Group struct {
	Title string
	Items []Item
}

type Item struct {
	Name, StatusClass, Basis, URL string
	Estimate                      bool
}

type Link struct{ Name, URL string }

type IngredientVM struct {
	Name, Category, Basis, EstimateNote string
	NotLocal                            string // shown when !Local: CopyNotLocal, or CopyNotGrown with seafood off
	Local, WildMushroom                 bool
	Bars                                []Bar
	TipChoose, TipStore, Note           string
	Pairings                            []Link
	BackURL                             string
}

type Bar struct {
	Letter, Title, Class string
	Current              bool
}

// StatusClass maps a status to its CSS class.
func StatusClass(s season.Status) string {
	switch s {
	case season.Peak:
		return "s-peak"
	case season.InSeason:
		return "s-in"
	case season.FromStorage:
		return "s-store"
	case season.YearRound:
		return "s-year"
	}
	return "s-out"
}

// MonthBars builds the 12-month bar, marking the current month.
func MonthBars(months [12]season.Status, current time.Month) []Bar {
	out := make([]Bar, 12)
	for m := 0; m < 12; m++ {
		name := time.Month(m + 1).String()
		out[m] = Bar{Letter: name[:1], Title: name + ": " + months[m].String(), Class: StatusClass(months[m]), Current: time.Month(m+1) == current}
	}
	return out
}
