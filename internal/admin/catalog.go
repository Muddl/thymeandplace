package admin

import (
	"net/http"
	"sort"
	"strings"

	"thymeandplace/internal/admin/views"
	"thymeandplace/internal/catalog"
	"thymeandplace/internal/changeset"
)

func (a *Admin) registerCatalog() {
	m := a.mux
	auth := func(f http.HandlerFunc) http.Handler { return a.requireAuth(f) }
	m.Handle("GET /admin", auth(a.catalogList))
	m.Handle("GET /admin/ingredients/new", auth(a.newForm))
	m.Handle("GET /admin/ingredients/fields", auth(a.kindFields))
	m.Handle("GET /admin/ingredients/{slug}", auth(a.editForm))
	m.Handle("POST /admin/ingredients/{slug}/preview", auth(a.formPreview))
	m.Handle("POST /admin/ingredients/{slug}/delete/preview", auth(a.deletePreview))
	m.Handle("GET /admin/import", auth(a.importPage))
	m.Handle("POST /admin/import", auth(a.importSubmit))
	m.Handle("POST /admin/changesets/{id}/apply", auth(a.apply))
	m.Handle("GET /admin/export", auth(a.export))
	m.Handle("GET /admin/history", auth(a.history))
}

func options[T ~string](all []T, selected string) []views.Option {
	out := make([]views.Option, len(all))
	for i, v := range all {
		out[i] = views.Option{Value: string(v), Label: string(v), Selected: string(v) == selected}
	}
	return out
}

func (a *Admin) loadSnap(w http.ResponseWriter, r *http.Request) (*catalog.Snapshot, bool) {
	s, err := a.d.Load(r.Context())
	if err != nil {
		a.d.Log.Error("catalog load failed", "err", err)
		http.Error(w, "Couldn't load the catalog. Try again in a moment.", http.StatusServiceUnavailable)
		return nil, false
	}
	return s, true
}

func (a *Admin) catalogList(w http.ResponseWriter, r *http.Request) {
	snap, ok := a.loadSnap(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	needle := strings.ToLower(strings.TrimSpace(q.Get("q")))
	vm := views.CatalogVM{
		Layout: a.layout(r, "Catalog"), Q: q.Get("q"), Category: q.Get("category"), Kind: q.Get("kind"),
		Categories: options(catalog.Categories, q.Get("category")), Kinds: options(catalog.Kinds, q.Get("kind")),
	}
	if q.Get("applied") != "" {
		vm.Message = "Changes applied."
	}
	for _, ing := range snap.All() {
		if needle != "" && !strings.Contains(strings.ToLower(ing.Name), needle) && !strings.Contains(ing.Slug, needle) {
			continue
		}
		if vm.Category != "" && string(ing.Category) != vm.Category {
			continue
		}
		if vm.Kind != "" && string(ing.Kind) != vm.Kind {
			continue
		}
		vm.Rows = append(vm.Rows, views.CatalogRow{Slug: ing.Slug, Name: ing.Name, Category: string(ing.Category), Kind: string(ing.Kind)})
	}
	sort.Slice(vm.Rows, func(i, j int) bool { return vm.Rows[i].Name < vm.Rows[j].Name })
	vm.Total = len(vm.Rows)
	if r.Header.Get("HX-Request") == "true" {
		a.render(w, r, http.StatusOK, views.CatalogRows(vm))
		return
	}
	a.render(w, r, http.StatusOK, views.CatalogPage(vm))
}

func (a *Admin) formVM(r *http.Request, isNew bool, values map[string]string, errs map[string][]string) views.FormVM {
	kind := values["kind"]
	if kind == "" {
		kind = string(catalog.KindAnnual)
		values["kind"] = kind
	}
	action := "/admin/ingredients/new/preview"
	if !isNew {
		action = "/admin/ingredients/" + values["slug"] + "/preview"
	}
	title := "Add ingredient"
	if !isNew {
		title = "Edit " + values["name"]
	}
	return views.FormVM{
		Layout: a.layout(r, title), IsNew: isNew, Action: action, Values: values, Errors: errs,
		Kind: kind, Fields: kindFields[catalog.Kind(kind)],
		Categories: options(catalog.Categories, values["category"]), Kinds: options(catalog.Kinds, kind),
	}
}

func (a *Admin) newForm(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, http.StatusOK, views.FormPage(a.formVM(r, true, map[string]string{"storage_months": "0", "frost": "tender"}, nil)))
}

func (a *Admin) editForm(w http.ResponseWriter, r *http.Request) {
	snap, ok := a.loadSnap(w, r)
	if !ok {
		return
	}
	ing, found := snap.Get(r.PathValue("slug"))
	if !found {
		http.NotFound(w, r)
		return
	}
	a.render(w, r, http.StatusOK, views.FormPage(a.formVM(r, false, formValues(ing), nil)))
}

// kindFields re-renders the kind-specific fieldset, keeping typed values.
func (a *Admin) kindFields(w http.ResponseWriter, r *http.Request) {
	values := map[string]string{}
	for k := range r.URL.Query() {
		values[k] = r.URL.Query().Get(k)
	}
	if values["frost"] == "" {
		values["frost"] = "tender"
	}
	a.render(w, r, http.StatusOK, views.KindFields(a.formVM(r, false, values, nil)))
}

// problemsToErrors maps problems to form fields; regions[i].* collapse onto "regions".
func (a *Admin) problemsToErrors(ps []catalog.Problem) map[string][]string {
	out := map[string][]string{}
	for _, p := range ps {
		field := p.Field
		if strings.HasPrefix(field, "regions") {
			msg := p.Message
			if field != "regions" {
				msg = field + " " + msg
			}
			out["regions"] = append(out["regions"], msg)
			continue
		}
		out[field] = append(out[field], p.Message)
	}
	return out
}

func formValuesFromRequest(r *http.Request) map[string]string {
	values := map[string]string{}
	for k := range r.PostForm {
		values[k] = r.PostForm.Get(k)
	}
	return values
}

func (a *Admin) formPreview(w http.ResponseWriter, r *http.Request) {
	isNew := r.PathValue("slug") == "new"
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Couldn't read the form.", http.StatusBadRequest)
		return
	}
	ing, problems := parseIngredientForm(r.PostForm)
	if !isNew {
		ing.Slug = r.PathValue("slug") // slugs are stable keys; the edit form can't rename
	}
	if isNew {
		if ing.Slug == "new" {
			problems = append(problems, catalog.Problem{Slug: ing.Slug, Field: "slug", Message: "“new” is reserved; choose another slug"})
		} else if snap, ok := a.loadSnap(w, r); !ok {
			return
		} else if _, exists := snap.Get(ing.Slug); exists {
			problems = append(problems, catalog.Problem{Slug: ing.Slug, Field: "slug", Message: "already exists; edit it instead"})
		}
	}
	// Parse problems win for their field; rule problems fill in the rest.
	parsed := map[string]bool{}
	for _, p := range problems {
		parsed[p.Field] = true
	}
	for _, p := range ing.Validate() {
		if !parsed[p.Field] {
			problems = append(problems, p)
		}
	}
	if len(problems) > 0 {
		a.render(w, r, http.StatusUnprocessableEntity, views.FormPage(a.formVM(r, isNew, formValuesFromRequest(r), a.problemsToErrors(problems))))
		return
	}
	a.previewChangeset(w, r, changeset.FormChangeset(ing), func(errs map[string][]string) {
		a.render(w, r, http.StatusUnprocessableEntity, views.FormPage(a.formVM(r, isNew, formValuesFromRequest(r), errs)))
	})
}

// importPage, importSubmit, export and history are the remaining write-side
// handlers; formPreview, deletePreview and apply (preview.go) are done. The
// rest are stubbed until Task 5.
func (a *Admin) importPage(w http.ResponseWriter, r *http.Request)   { http.NotFound(w, r) }
func (a *Admin) importSubmit(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }
func (a *Admin) export(w http.ResponseWriter, r *http.Request)       { http.NotFound(w, r) }
func (a *Admin) history(w http.ResponseWriter, r *http.Request)      { http.NotFound(w, r) }
