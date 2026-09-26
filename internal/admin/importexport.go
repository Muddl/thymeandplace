package admin

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"

	"thymeandplace/internal/admin/views"
	"thymeandplace/internal/catalog/catalogjson"
	"thymeandplace/internal/changeset"
	"thymeandplace/internal/store"
)

func (a *Admin) importPage(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, http.StatusOK, views.ImportPage(views.ImportVM{Layout: a.layout(r, "Import")}))
}

func (a *Admin) importError(w http.ResponseWriter, r *http.Request, status int, msg string, problems []string) {
	a.render(w, r, status, views.ImportPage(views.ImportVM{Layout: a.layout(r, "Import"), Message: msg, Problems: problems}))
}

func (a *Admin) importSubmit(w http.ResponseWriter, r *http.Request) {
	f, fh, err := r.FormFile("file")
	if err != nil {
		a.importError(w, r, http.StatusBadRequest, "Choose a JSON file to import.", nil)
		return
	}
	defer f.Close()
	if fh.Size > catalogjson.MaxBytes {
		a.importError(w, r, http.StatusBadRequest, catalogjson.ErrTooLarge.Error()+".", nil)
		return
	}
	cs, err := changeset.ImportChangeset(f)
	if err != nil {
		msg := err.Error()
		if errors.Is(err, catalogjson.ErrTooLarge) {
			msg = catalogjson.ErrTooLarge.Error()
		}
		a.importError(w, r, http.StatusBadRequest, "The file can't be imported: "+msg+".", nil)
		return
	}
	a.previewImport(w, r, cs)
}

func (a *Admin) previewImport(w http.ResponseWriter, r *http.Request, cs changeset.Changeset) {
	p, pv, err := a.d.Changes.Preview(r.Context(), cs)
	var inv *changeset.InvalidError
	switch {
	case errors.As(err, &inv):
		var lines []string
		for _, pr := range inv.Problems {
			lines = append(lines, pr.String())
		}
		a.importError(w, r, http.StatusUnprocessableEntity, "Nothing was imported. Fix these problems and upload again:", lines)
	case err != nil:
		a.d.Log.Error("import preview failed", "err", err)
		a.importError(w, r, http.StatusInternalServerError, "Couldn't build the preview. Try again.", nil)
	default:
		a.renderPreview(w, r, http.StatusOK, p, pv, "")
	}
}

func (a *Admin) export(w http.ResponseWriter, r *http.Request) {
	snap, ok := a.loadSnap(w, r)
	if !ok {
		return
	}
	now := a.d.Auth.Now().UTC()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="thymeandplace-catalog-%s.json"`, now.Format("20060102")))
	if err := changeset.Export(w, snap, now); err != nil {
		a.d.Log.Error("export failed", "err", err)
	}
}

func (a *Admin) history(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	params := store.ListHistoryParams{}
	if s := q.Get("slug"); s != "" {
		params.Slug = pgtype.Text{String: s, Valid: true}
	}
	if bs := q.Get("before"); bs != "" {
		b, err := strconv.ParseInt(bs, 10, 64)
		if err != nil {
			http.Error(w, "The \"before\" cursor is not valid.", http.StatusBadRequest)
			return
		}
		params.Before = pgtype.Int8{Int64: b, Valid: true}
	}
	rows, err := store.New(a.d.Pool).ListHistory(r.Context(), params)
	if err != nil {
		a.d.Log.Error("history failed", "err", err)
		http.Error(w, "Couldn't load history.", http.StatusInternalServerError)
		return
	}
	vm := views.HistoryVM{Layout: a.layout(r, "History"), Slug: q.Get("slug")}
	for _, h := range rows {
		row := views.HistoryRow{
			ID: h.ID, At: h.ChangedAt.Time.UTC().Format("2006-01-02 15:04:05"), Actor: h.Actor, Action: h.Action,
			Table: h.TableName, Key: string(h.RowKey), Before: string(h.Before), After: string(h.After),
		}
		if h.ChangeID.Valid {
			row.ChangeID = "#" + strconv.FormatInt(h.ChangeID.Int64, 10)
		}
		vm.Rows = append(vm.Rows, row)
	}
	if len(rows) == 100 {
		v := url.Values{"before": {strconv.FormatInt(rows[len(rows)-1].ID, 10)}}
		if vm.Slug != "" {
			v.Set("slug", vm.Slug)
		}
		vm.OlderURL = "/admin/history?" + v.Encode()
	}
	a.render(w, r, http.StatusOK, views.HistoryPage(vm))
}
