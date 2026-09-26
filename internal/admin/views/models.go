// Package views holds the admin templ components.
package views

import (
	"encoding/json"
	"strconv"
)

const htmxConfig = `{"responseHandling":[{"code":"204","swap":false},{"code":"[23]..","swap":true},{"code":"[45]..","swap":true,"error":true}],"includeIndicatorStyles":false}`

type LayoutVM struct {
	Title    string
	CSRF     string
	SignedIn bool
}

// csrfHeaders is the hx-headers value for <body>.
func csrfHeaders(token string) string {
	b, _ := json.Marshal(map[string]string{"X-CSRF-Token": token})
	return string(b)
}

type LoginVM struct {
	Layout        LayoutVM
	BackupEnabled bool
	Message       string
}

type BackupVM struct {
	Layout  LayoutVM
	Message string
}

type EnrollVM struct {
	Layout LayoutVM
	Token  string
	Valid  bool
}

type PasskeyRow struct {
	ID, Name, Created, LastUsed string
}

type SecurityVM struct {
	Layout        LayoutVM
	Passkeys      []PasskeyRow
	RecentAuth    bool
	BackupEnabled bool
	Message       string
}

type Option struct {
	Value, Label string
	Selected     bool
}

type CatalogRow struct{ Slug, Name, Category, Kind string }

type CatalogVM struct {
	Layout            LayoutVM
	Q, Category, Kind string
	Categories, Kinds []Option
	Rows              []CatalogRow
	Total             int
	Message           string
}

type FormVM struct {
	Layout     LayoutVM
	IsNew      bool
	Action     string
	Values     map[string]string
	Errors     map[string][]string
	Kind       string
	Fields     []string // kind-specific fields to show
	Categories []Option
	Kinds      []Option
	Message    string
}

func (vm FormVM) Has(field string) bool {
	for _, f := range vm.Fields {
		if f == field {
			return true
		}
	}
	return false
}

// plural formats a count with the given noun: "1 ingredient" / "2 ingredients".
func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}

func itoa(i int64) string { return strconv.FormatInt(i, 10) }

type FieldChange struct{ Field, Before, After string }

type ChangedRow struct {
	Slug, Name string
	Fields     []FieldChange
}

type ItemRow struct{ Slug, Name string }

type BarCell struct{ Class, Title string }

type BarRow struct {
	Fixture             string
	Before, After       []BarCell
	HadBefore, HasAfter bool
}

type ScoreRow struct {
	Slug, Name, Before, After string
	Bars                      []BarRow
}

type PreviewVM struct {
	Layout                  LayoutVM
	ID                      int64
	Source, DeleteSlug      string
	Added, Deleted          []ItemRow
	Changed                 []ChangedRow
	Unchanged               int
	ScoreBefore, ScoreAfter string
	Delta                   string
	Compared                int
	NeedsOverride           bool
	Items                   []ScoreRow
	Message                 string
	ExpiresAt               string
}
