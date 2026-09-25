// Package changeset is the single path for catalog edits:
// build → validate → preview → apply.
package changeset

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogjson"
	"thymeandplace/internal/ocean"
)

const (
	SourceForm   = "form"
	SourceDelete = "delete"
	SourceImport = "import"
)

// Changeset is a set of upserts and deletes. It is stored as JSON in
// pending_changeset between preview and apply.
type Changeset struct {
	Source  string
	Upserts []catalog.Ingredient
	Deletes []string
}

func FormChangeset(ing catalog.Ingredient) Changeset {
	return Changeset{Source: SourceForm, Upserts: []catalog.Ingredient{ing}}
}

func DeleteChangeset(slug string) Changeset {
	return Changeset{Source: SourceDelete, Deletes: []string{slug}}
}

// ImportChangeset decodes a catalog file. Import never deletes.
func ImportChangeset(r io.Reader) (Changeset, error) {
	items, err := catalogjson.Decode(r)
	if err != nil {
		return Changeset{}, err
	}
	return Changeset{Source: SourceImport, Upserts: items}, nil
}

// Export writes the whole snapshot in the import format.
func Export(w io.Writer, snap *catalog.Snapshot, now time.Time) error {
	return catalogjson.Encode(w, snap.All(), now)
}

type EcoregionChecker interface {
	Missing(ctx context.Context, ids []ocean.EcoregionID) ([]ocean.EcoregionID, error)
}

var (
	ErrStale            = errors.New("changeset: catalog changed since preview")
	ErrNotFound         = errors.New("changeset: preview expired or already applied")
	ErrOverrideRequired = errors.New("changeset: calibration override required")
	ErrConfirmMismatch  = errors.New("changeset: delete confirmation does not match")
	ErrNotReady         = errors.New("changeset: catalog not loaded")
)

// InvalidError carries per-item, per-field problems. Nothing was stored.
type InvalidError struct{ Problems []catalog.Problem }

func (e *InvalidError) Error() string {
	return (&catalog.ValidationError{Problems: e.Problems}).Error()
}

// UserMessage is the owner-facing text for a changeset error, or "".
func UserMessage(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrStale):
		return "The catalog changed since this preview. Preview again."
	case errors.Is(err, ErrNotFound):
		return "This preview has expired or was already applied. Preview again."
	case errors.Is(err, ErrOverrideRequired):
		return "The calibration score drops by more than 0.02. Tick “Apply despite lower calibration score” to apply anyway."
	case errors.Is(err, ErrConfirmMismatch):
		return "Type the ingredient's slug exactly to confirm the delete."
	}
	return ""
}

// Validate checks cs against cur and live ecoregions. It returns the
// proposed snapshot when there are no problems. err is non-nil only for
// infrastructure failures.
func Validate(ctx context.Context, cs Changeset, cur *catalog.Snapshot, eco EcoregionChecker) (*catalog.Snapshot, []catalog.Problem, error) {
	var problems []catalog.Problem
	seen := map[string]bool{}
	ups := make([]catalog.Ingredient, len(cs.Upserts))
	for i, u := range cs.Upserts {
		ups[i] = catalog.Normalize(u)
		problems = append(problems, ups[i].Validate()...)
		if seen[u.Slug] {
			problems = append(problems, catalog.Problem{Slug: u.Slug, Field: "slug", Message: "appears more than once in this change"})
		}
		seen[u.Slug] = true
	}
	for _, d := range cs.Deletes {
		if seen[d] {
			problems = append(problems, catalog.Problem{Slug: d, Field: "slug", Message: "cannot be both saved and deleted in one change"})
		}
	}
	if len(problems) > 0 {
		return nil, problems, nil
	}

	proposed, err := cur.With(ups, cs.Deletes)
	var ve *catalog.ValidationError
	if errors.As(err, &ve) {
		return nil, ve.Problems, nil
	}
	if err != nil {
		return nil, nil, err
	}

	type loc struct {
		slug string
		i    int
	}
	var ids []ocean.EcoregionID
	owners := map[ocean.EcoregionID][]loc{}
	for _, u := range ups {
		for i, r := range u.Regions {
			ids = append(ids, r.EcoregionID)
			owners[r.EcoregionID] = append(owners[r.EcoregionID], loc{u.Slug, i})
		}
	}
	if len(ids) > 0 {
		missing, err := eco.Missing(ctx, ids)
		if err != nil {
			return nil, nil, err
		}
		for _, m := range missing {
			for _, o := range owners[m] {
				problems = append(problems, catalog.Problem{
					Slug: o.slug, Field: fmt.Sprintf("regions[%d].ecoregion_id", o.i),
					Message: fmt.Sprintf("ecoregion %d does not exist in the live ocean data", m),
				})
			}
		}
	}
	if len(problems) > 0 {
		return nil, problems, nil
	}
	return proposed, nil, nil
}
