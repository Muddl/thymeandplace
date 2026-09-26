// Package views holds the templ components for the public site.
package views

import (
	"time"

	"github.com/a-h/templ"

	"thymeandplace/internal/season"
)

// User-facing copy (spec §11.6). Tests assert these exact strings.
const (
	CopyNoMatchSuffix      = "Try a nearby larger town or city."
	CopyGeoOff             = "Location access is off, so search for your town instead."
	CopyUnsupported        = "We can't estimate seasons here yet. Estimates cover land and coastal waters."
	CopySearchAnother      = "Search another place"
	CopyHarsh              = "Not much grows locally this time of year. Here's what's still around."
	CopyLoadError          = "Couldn't load seasonal picks right now. Try again in a moment."
	CopyTryAgain           = "Try again"
	CopyPaused             = "Search is pausing for a few seconds. Keep typing and results will catch up."
	CopySearchUnavailable  = "Search isn't available right now. Try again in a moment."
	CopyNotFound           = "We couldn't find that page."
	WildMushroomDisclaimer = "Look for wild mushrooms at markets and from sellers who can identify them. We don't give foraging advice: many edible mushrooms have deadly look-alikes."
	CopyLandEstimate       = "Estimated from local climate normals."
)

// HTMX config: swap 4xx/5xx so error partials render (§11.5).
const htmxConfig = `{"responseHandling":[{"code":"204","swap":false},{"code":"[23]..","swap":true},{"code":"[45]..","swap":true,"error":true}],"includeIndicatorStyles":false}`

type PageVM struct {
	Title   string
	Query   string
	Places  templ.Component
	Results templ.Component
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
