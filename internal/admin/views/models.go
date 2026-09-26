// Package views holds the admin templ components.
package views

import "encoding/json"

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
