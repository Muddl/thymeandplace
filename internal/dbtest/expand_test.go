package dbtest

import (
	"os"
	"strings"
	"testing"
)

func TestExpandPsqlVars(t *testing.T) {
	got, err := expandPsqlVars(
		"CREATE ROLE x PASSWORD :'owner_password'; ALTER DATABASE :\"db\" OWNER TO tp_owner;",
		map[string]string{"owner_password": "p'w", "db": "tp_template"},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := "CREATE ROLE x PASSWORD 'p''w'; ALTER DATABASE \"tp_template\" OWNER TO tp_owner;"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	_, err = expandPsqlVars("PASSWORD :'owner_password'", map[string]string{})
	if err == nil || !strings.Contains(err.Error(), "owner_password") {
		t.Fatalf("unset variable: got %v", err)
	}
}

func TestProductionRolesSQLHasLeastPrivilegeFlags(t *testing.T) {
	raw, err := os.ReadFile(ProductionRolesSQL)
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, role := range []string{"tp_owner", "tp_app", "tp_backup"} {
		if !strings.Contains(src, "CREATE ROLE "+role+" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE") {
			t.Errorf("%s: missing least-privilege CREATE ROLE for %s", ProductionRolesSQL, role)
		}
	}
	if !strings.Contains(src, "GRANT pg_read_all_data TO tp_backup") {
		t.Errorf("%s: missing backup grant", ProductionRolesSQL)
	}
}
