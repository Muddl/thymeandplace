package admin

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"thymeandplace/internal/admin/views"
	"thymeandplace/internal/changeset"
	"thymeandplace/internal/season"
)

func score(p *float64) string {
	if p == nil {
		return "—"
	}
	return fmt.Sprintf("%.3f", *p)
}

func barCells(months [12]season.Status) []views.BarCell {
	out := make([]views.BarCell, 12)
	for m := 0; m < 12; m++ {
		name := time.Month(m + 1).String()
		cls := map[season.Status]string{season.Peak: "s-peak", season.InSeason: "s-in", season.FromStorage: "s-store", season.YearRound: "s-year"}[months[m]]
		out[m] = views.BarCell{Class: cls, Title: name + ": " + months[m].String()}
	}
	return out
}

func previewVM(layout views.LayoutVM, p changeset.Pending, pv changeset.Preview, msg string) views.PreviewVM {
	vm := views.PreviewVM{
		Layout: layout, ID: p.ID, Source: p.Changeset.Source, Unchanged: pv.Diff.Unchanged,
		ScoreBefore: fmt.Sprintf("%.3f", pv.Before.Score), ScoreAfter: fmt.Sprintf("%.3f", pv.After.Score),
		Delta: fmt.Sprintf("%+.3f", pv.Delta), Compared: max(pv.Before.Compared, pv.After.Compared),
		NeedsOverride: pv.NeedsOverride, Message: msg, ExpiresAt: p.ExpiresAt.UTC().Format("15:04"),
	}
	if p.Changeset.Source == changeset.SourceDelete && len(p.Changeset.Deletes) == 1 {
		vm.DeleteSlug = p.Changeset.Deletes[0]
	}
	for _, a := range pv.Diff.Added {
		vm.Added = append(vm.Added, views.ItemRow{Slug: a.Slug, Name: a.Name})
	}
	for _, d := range pv.Diff.Deleted {
		vm.Deleted = append(vm.Deleted, views.ItemRow{Slug: d.Slug, Name: d.Name})
	}
	for _, c := range pv.Diff.Changed {
		row := views.ChangedRow{Slug: c.Slug, Name: c.Name}
		for _, f := range c.Fields {
			row.Fields = append(row.Fields, views.FieldChange{Field: f.Field, Before: f.Before, After: f.After})
		}
		vm.Changed = append(vm.Changed, row)
	}
	for _, it := range pv.Items {
		sr := views.ScoreRow{Slug: it.Slug, Name: it.Name, Before: score(it.ScoreBefore), After: score(it.ScoreAfter)}
		for _, b := range it.Bars {
			sr.Bars = append(sr.Bars, views.BarRow{Fixture: b.Fixture, Before: barCells(b.Before), After: barCells(b.After), HadBefore: b.HadBefore, HasAfter: b.HasAfter})
		}
		vm.Items = append(vm.Items, sr)
	}
	return vm
}

func (a *Admin) renderPreview(w http.ResponseWriter, r *http.Request, status int, p changeset.Pending, pv changeset.Preview, msg string) {
	a.render(w, r, status, views.PreviewPage(previewVM(a.layout(r, "Preview"), p, pv, msg)))
}

// previewChangeset validates, stores and renders a preview. Validation
// problems go to onInvalid so each caller can show them in context.
func (a *Admin) previewChangeset(w http.ResponseWriter, r *http.Request, cs changeset.Changeset, onInvalid func(map[string][]string)) {
	p, pv, err := a.d.Changes.Preview(r.Context(), cs)
	var inv *changeset.InvalidError
	switch {
	case errors.As(err, &inv):
		onInvalid(a.problemsToErrors(inv.Problems))
	case err != nil:
		a.d.Log.Error("preview failed", "err", err)
		http.Error(w, "Couldn't build the preview. Try again.", http.StatusInternalServerError)
	default:
		a.renderPreview(w, r, http.StatusOK, p, pv, "")
	}
}

func (a *Admin) deletePreview(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	a.previewChangeset(w, r, changeset.DeleteChangeset(slug), func(errs map[string][]string) {
		http.Error(w, "Can't delete "+slug+": it does not exist.", http.StatusNotFound)
	})
}
