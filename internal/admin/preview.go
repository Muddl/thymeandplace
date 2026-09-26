package admin

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"thymeandplace/internal/admin/views"
	"thymeandplace/internal/changeset"
	"thymeandplace/internal/season"
	"thymeandplace/internal/store"
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
		Empty: len(pv.Diff.Added) == 0 && len(pv.Diff.Changed) == 0 && len(pv.Diff.Deleted) == 0,
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
		a.messagePage(w, r, http.StatusNotFound, "Can't delete "+slug+": it does not exist.")
	})
}

func (a *Admin) apply(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	sess, _ := SessionFrom(r.Context())
	opts := changeset.ApplyOptions{Override: r.PostFormValue("override") == "1", ConfirmSlug: strings.TrimSpace(r.PostFormValue("confirm_slug"))}
	err = a.d.Changes.Apply(r.Context(), id, "owner ("+sess.Method+")", opts)
	var inv *changeset.InvalidError
	switch {
	case err == nil:
		http.Redirect(w, r, "/admin?applied=1", http.StatusSeeOther)
	case errors.Is(err, changeset.ErrOverrideRequired), errors.Is(err, changeset.ErrConfirmMismatch):
		p, pv, rerr := a.d.Changes.Reopen(r.Context(), id)
		if rerr != nil {
			a.reopenFailed(w, r, id, rerr, p)
			return
		}
		a.renderPreview(w, r, http.StatusUnprocessableEntity, p, pv, changeset.UserMessage(err))
	case errors.Is(err, changeset.ErrStale):
		// The pending row still exists (only the base version is stale), so
		// it can be reopened to learn which slug this changeset concerned,
		// and offer a link straight back to it.
		p, _, rerr := a.d.Changes.Reopen(r.Context(), id)
		if rerr != nil && !errors.Is(rerr, changeset.ErrStale) {
			a.reopenFailed(w, r, id, rerr, p)
			return
		}
		a.messagePage(w, r, http.StatusConflict, changeset.UserMessage(err), slugLinks(p.Changeset)...)
	case errors.Is(err, changeset.ErrNotFound):
		// A double-clicked Apply button (or a resubmit of an old tab) sends
		// this request twice; the first request already deleted the pending
		// row. Tell that apart from a genuinely expired preview by checking
		// whether this change_id already wrote history: if it did, the first
		// click succeeded and this one should land exactly where a first
		// success would.
		applied, aerr := store.New(a.d.Pool).ChangeApplied(r.Context(), id)
		if aerr != nil {
			a.d.Log.Error("check change applied failed", "err", aerr, "changeset", id)
			http.Error(w, "Couldn't apply the change. Try again.", http.StatusInternalServerError)
			return
		}
		if applied {
			http.Redirect(w, r, "/admin?applied=1", http.StatusSeeOther)
			return
		}
		a.messagePage(w, r, http.StatusGone, changeset.UserMessage(err))
	case errors.As(err, &inv):
		msgs := []string{}
		for _, p := range inv.Problems {
			msgs = append(msgs, p.String())
		}
		a.messagePage(w, r, http.StatusConflict, "The change is no longer valid: "+strings.Join(msgs, "; ")+". Preview again.")
	default:
		a.d.Log.Error("apply failed", "err", err, "changeset", id)
		a.messagePage(w, r, http.StatusInternalServerError, "Couldn't apply the change. Nothing was written. Try again.")
	}
}

// reopenFailed reports a failed Reopen with a message that is never blank:
// ErrStale/ErrNotFound get their normal owner-facing text (and status), and
// anything else (an infrastructure failure) is logged and reported as a 500.
func (a *Admin) reopenFailed(w http.ResponseWriter, r *http.Request, id int64, err error, p changeset.Pending) {
	switch {
	case errors.Is(err, changeset.ErrStale):
		a.messagePage(w, r, http.StatusConflict, changeset.UserMessage(err), slugLinks(p.Changeset)...)
	case errors.Is(err, changeset.ErrNotFound):
		a.messagePage(w, r, http.StatusGone, changeset.UserMessage(err))
	default:
		a.d.Log.Error("reopen changeset failed", "err", err, "changeset", id)
		a.messagePage(w, r, http.StatusInternalServerError, "Couldn't reopen the preview. Try again.")
	}
}

// slugLinks returns a link to the ingredient's edit form when cs concerns
// exactly one slug (a single-item form save or a single delete), or nil
// when the changeset spans several slugs (e.g. an import) or is unknown.
func slugLinks(cs changeset.Changeset) []views.Link {
	switch {
	case len(cs.Upserts) == 1 && len(cs.Deletes) == 0:
		slug := cs.Upserts[0].Slug
		return []views.Link{{Href: "/admin/ingredients/" + slug, Label: "Edit " + slug}}
	case len(cs.Deletes) == 1 && len(cs.Upserts) == 0:
		slug := cs.Deletes[0]
		return []views.Link{{Href: "/admin/ingredients/" + slug, Label: "Edit " + slug}}
	default:
		return nil
	}
}

func (a *Admin) messagePage(w http.ResponseWriter, r *http.Request, status int, msg string, links ...views.Link) {
	a.render(w, r, status, views.MessagePage(a.layout(r, "Catalog"), msg, links))
}
