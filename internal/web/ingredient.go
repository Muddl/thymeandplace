package web

import (
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
	snap := s.d.Snapshot()
	if snap == nil {
		s.render(w, r, http.StatusServiceUnavailable, "Ingredient", "", nil, views.LoadError("", r.URL.RequestURI()))
		return
	}
	ing, ok := snap.Get(r.PathValue("slug"))
	if !ok {
		s.render(w, r, http.StatusNotFound, "Not found", "", nil, views.NotFound())
		return
	}
	q := r.URL.Query()
	cell, err := strconv.Atoi(q.Get("cell"))
	if err != nil || !climate.ValidCellID(cell) {
		s.render(w, r, http.StatusBadRequest, ing.Name, "", nil, views.Unsupported())
		return
	}
	SetCellID(ctx, cell)
	month := s.monthFor(q.Get("date"))
	date := validDate(q.Get("date"))

	normals, err := s.d.Climate.ForCell(ctx, cell)
	if err != nil {
		s.d.Log.Warn("ingredient lookup failed", "err", err, "request_id", RequestIDFrom(ctx), "cell_id", cell)
		s.render(w, r, http.StatusServiceUnavailable, ing.Name, "", nil, views.LoadError("", r.URL.RequestURI()))
		return
	}
	lat, lng := climate.CellCenter(cell)
	eco, err := s.d.Ocean.Nearby(ctx, lat, lng)
	if err != nil {
		s.d.Log.Warn("ingredient lookup failed", "err", err, "request_id", RequestIDFrom(ctx), "cell_id", cell)
		s.render(w, r, http.StatusServiceUnavailable, ing.Name, "", nil, views.LoadError("", r.URL.RequestURI()))
		return
	}
	months, basis, local := season.Year(season.Input{Normals: normals, Ecoregions: eco, Month: month}, ing)

	vm := views.IngredientVM{
		Name: ing.Name, Category: string(ing.Category), Basis: basis, Local: local,
		WildMushroom: ing.Kind == catalog.KindWildMushroom,
		Bars:         views.MonthBars(months, month),
		TipChoose:    ing.TipChoose, TipStore: ing.TipStore, Note: ing.Note,
		BackURL: fmt.Sprintf("/season?lat=%.4f&lng=%.4f", lat, lng),
	}
	if date != "" {
		vm.BackURL += "&date=" + date
	}
	if ing.Kind != catalog.KindSeafood {
		vm.EstimateNote = views.CopyLandEstimate
	}
	for _, p := range ing.Pairings {
		if other, ok := snap.Get(p); ok {
			vm.Pairings = append(vm.Pairings, views.Link{Name: other.Name, URL: ingredientURL(p, cell, date)})
		}
	}
	s.render(w, r, http.StatusOK, ing.Name, "", nil, views.Ingredient(vm))
}
