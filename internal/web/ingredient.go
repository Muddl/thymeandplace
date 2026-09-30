package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/climate"
	"thymeandplace/internal/season"
	"thymeandplace/internal/web/views"
)

func (s *Server) ingredient(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	// Read the version before the snapshot, as /season does: the watcher
	// swaps the snapshot before publishing a new version, so this order never
	// caches old inputs under a new version key.
	version := s.d.DataVersion()
	snap := s.d.Snapshot()
	if snap == nil {
		s.render(w, r, http.StatusServiceUnavailable, "Ingredient", "", nil, views.LoadError("", r.URL.RequestURI()))
		return
	}
	ing, ok := snap.Get(r.PathValue("slug"))
	if !ok || !s.visible(ing) { // a seafood item with seafood off doesn't exist publicly
		s.render(w, r, http.StatusNotFound, "Not found", "", nil, views.NotFound())
		return
	}
	q := r.URL.Query()
	cell, err := strconv.Atoi(q.Get("cell"))
	if err != nil || !climate.ValidCellID(cell) {
		s.render(w, r, http.StatusBadRequest, ing.Name, "", nil, s.unsupported())
		return
	}
	// cell is the query cell of the /season page that linked here (core spec §11.1).
	SetCellID(ctx, cell)
	month := s.monthFor(q.Get("date"))
	date := s.validDate(q.Get("date"))

	in, err := s.inputsFor(ctx, cell, version)
	if errors.Is(err, errBusy) {
		s.overloaded(w, r, ing.Name, "", r.URL.RequestURI())
		return
	}
	if err != nil {
		s.d.Log.Warn("ingredient lookup failed", "err", err, "request_id", RequestIDFrom(ctx), "cell_id", cell)
		s.render(w, r, http.StatusServiceUnavailable, ing.Name, "", nil, views.LoadError("", r.URL.RequestURI()))
		return
	}
	lat, lng := climate.CellCenter(cell)
	months, basis, local := season.Year(in.Engine(month), ing)

	vm := views.IngredientVM{
		Name: ing.Name, Category: string(ing.Category), Basis: basis, Local: local,
		WildMushroom: ing.Kind == catalog.KindWildMushroom,
		Bars:         views.MonthBars(months, month),
		TipChoose:    ing.TipChoose, TipStore: ing.TipStore, Note: ing.Note,
		BackURL:  fmt.Sprintf("/season?lat=%.4f&lng=%.4f", lat, lng),
		NotLocal: views.CopyNotGrown,
	}
	if s.d.Seafood {
		vm.NotLocal = views.CopyNotLocal
	}
	if date != "" {
		vm.BackURL += "&date=" + date
	}
	if ing.Kind != catalog.KindSeafood {
		vm.EstimateNote = views.CopyLandEstimate
	}
	for _, p := range ing.Pairings {
		if other, ok := snap.Get(p); ok && s.visible(other) {
			vm.Pairings = append(vm.Pairings, views.Link{Name: other.Name, URL: ingredientURL(p, cell, date)})
		}
	}
	s.renderPage(w, r, http.StatusOK, ing.Name, views.IngredientDescription(ing.Name), "", nil, views.Ingredient(vm))
}
