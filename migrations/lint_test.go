package migrations

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShippedMigrationsMatchChecksums(t *testing.T) {
	if err := verifyChecksums("."); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyChecksumsRejectsEditsAndGaps(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("00001_ok.sql", "SELECT 1;\n")
	sum, err := fileSHA256(filepath.Join(dir, "00001_ok.sql"))
	if err != nil {
		t.Fatal(err)
	}
	write(checksumsName, sum+"  00001_ok.sql\n")
	if err := verifyChecksums(dir); err != nil {
		t.Fatalf("matching pin: %v", err)
	}

	write("00001_ok.sql", "SELECT 2;\n")
	err = verifyChecksums(dir)
	if err == nil || !strings.Contains(err.Error(), "00001_ok.sql") || !strings.Contains(err.Error(), "do not edit a shipped migration") {
		t.Fatalf("edited file: want a shipped-migration error naming 00001_ok.sql, got %v", err)
	}

	write("00001_ok.sql", "SELECT 1;\n")
	write("00002_new.sql", "SELECT 3;\n")
	err = verifyChecksums(dir)
	if err == nil || !strings.Contains(err.Error(), "00002_new.sql is not pinned") {
		t.Fatalf("unpinned file: want a pin line for 00002_new.sql, got %v", err)
	}

	write(checksumsName, sum+"  00001_ok.sql\n"+"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  missing.sql\n")
	err = verifyChecksums(dir)
	if err == nil || !strings.Contains(err.Error(), "missing.sql is pinned") {
		t.Fatalf("extra pin: want missing.sql reported, got %v", err)
	}
}

func TestShippedUpSectionsHaveContractMarkers(t *testing.T) {
	if err := verifyContracts("."); err != nil {
		t.Fatal(err)
	}
}

func TestContractViolations(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{
			name: "up drop without marker",
			src:  "-- +goose Up\nDROP TABLE leftover;\n-- +goose Down\nSELECT 1;\n",
			want: 1,
		},
		{
			name: "marker on previous line",
			src:  "-- +goose Up\n-- contract: leftover is unused after ingest v2\nDROP TABLE leftover;\n",
			want: 0,
		},
		{
			name: "marker on same line",
			src:  "-- +goose Up\nDROP TABLE leftover; -- contract: leftover is unused after ingest v2\n",
			want: 0,
		},
		{
			name: "down drop needs no marker",
			src:  "-- +goose Up\nSELECT 1;\n-- +goose Down\nDROP TABLE leftover;\n",
			want: 0,
		},
		{
			name: "rename without marker",
			src:  "-- +goose Up\nALTER TABLE place RENAME TO place_old;\n",
			want: 1,
		},
		{
			name: "comment that mentions DROP is not a statement",
			src:  "-- +goose Up\n-- DROP is in the down section\nSELECT 1;\n",
			want: 0,
		},
		{
			name: "drop column without marker",
			src:  "-- +goose Up\nALTER TABLE place DROP COLUMN unused;\n",
			want: 1,
		},
		{
			name: "blank line after marker still counts",
			src:  "-- +goose Up\n-- contract: leftover is unused\n\nDROP TABLE leftover;\n",
			want: 0,
		},
		{
			name: "other statement clears the marker",
			src:  "-- +goose Up\n-- contract: leftover is unused\nSELECT 1;\nDROP TABLE leftover;\n",
			want: 1,
		},
		{
			name: "alter column type with COLUMN",
			src:  "-- +goose Up\nALTER TABLE place ALTER COLUMN name TYPE text;\n",
			want: 1,
		},
		{
			name: "alter column type without COLUMN",
			src:  "-- +goose Up\nALTER TABLE place ALTER name TYPE text;\n",
			want: 1,
		},
		{
			name: "alter column type mixed case",
			src:  "-- +goose Up\nAlter Table place Alter Column name Type text;\n",
			want: 1,
		},
		{
			name: "alter column type multi-line with COLUMN",
			src:  "-- +goose Up\nALTER TABLE place\n  ALTER COLUMN name TYPE text;\n",
			want: 1,
		},
		{
			name: "alter column type multi-line without COLUMN",
			src:  "-- +goose Up\nALTER TABLE place\n  ALTER name TYPE text;\n",
			want: 1,
		},
		{
			name: "alter column set not null with COLUMN",
			src:  "-- +goose Up\nALTER TABLE place ALTER COLUMN name SET NOT NULL;\n",
			want: 1,
		},
		{
			name: "alter column set not null without COLUMN",
			src:  "-- +goose Up\nALTER TABLE place ALTER name SET NOT NULL;\n",
			want: 1,
		},
		{
			name: "alter column set not null mixed case",
			src:  "-- +goose Up\nalter table place alter column name set not null;\n",
			want: 1,
		},
		{
			name: "alter column set not null multi-line with COLUMN",
			src:  "-- +goose Up\nALTER TABLE place\n  ALTER COLUMN name SET NOT NULL;\n",
			want: 1,
		},
		{
			name: "alter column set not null multi-line without COLUMN",
			src:  "-- +goose Up\nALTER TABLE place\n  ALTER name SET NOT NULL;\n",
			want: 1,
		},
		{
			name: "rename column without COLUMN keyword",
			src:  "-- +goose Up\nALTER TABLE place RENAME name TO title;\n",
			want: 1,
		},
		{
			name: "rename column without COLUMN mixed case",
			src:  "-- +goose Up\nalter table place rename name to title;\n",
			want: 1,
		},
		{
			name: "rename column without COLUMN multi-line",
			src:  "-- +goose Up\nALTER TABLE place\n  RENAME name TO title;\n",
			want: 1,
		},
		{
			name: "add nullable column",
			src:  "-- +goose Up\nALTER TABLE place ADD COLUMN extra text;\n",
			want: 0,
		},
		{
			name: "add nullable column multi-line",
			src:  "-- +goose Up\nALTER TABLE place\n  ADD COLUMN extra text;\n",
			want: 0,
		},
		{
			name: "drop not null with COLUMN",
			src:  "-- +goose Up\nALTER TABLE place ALTER COLUMN name DROP NOT NULL;\n",
			want: 0,
		},
		{
			name: "drop not null without COLUMN",
			src:  "-- +goose Up\nALTER TABLE place ALTER name DROP NOT NULL;\n",
			want: 0,
		},
		{
			name: "drop not null multi-line",
			src:  "-- +goose Up\nALTER TABLE place\n  ALTER COLUMN name DROP NOT NULL;\n",
			want: 0,
		},
		{
			name: "marked type change",
			src:  "-- +goose Up\n-- contract: previous release does not read name\nALTER TABLE place ALTER COLUMN name TYPE text;\n",
			want: 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := contractViolations("x.sql", c.src)
			if len(got) != c.want {
				t.Fatalf("got %d violations %v, want %d", len(got), got, c.want)
			}
		})
	}
}
