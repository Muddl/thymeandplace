package web

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"thymeandplace/internal/cache"
	"thymeandplace/internal/catalog"
	"thymeandplace/internal/cellinput"
	"thymeandplace/internal/climate"
	"thymeandplace/internal/season"
	"thymeandplace/internal/web/views"
)

const (
	harshThreshold = 3  // fewer land items at peak/in season than this → "not much grows"
	nearKm         = 50 // "near X" only when X is this close
)

// seasonCore is the cached part of /season. It depends only on its key
// (query cell, month, data version): every input comes from the query cell
// (internal/cellinput), never from the request's exact point. The heading
// label is not part of it; it is looked up per request.
type seasonCore struct {
	QueryCell  int
	HasNormals bool
	Supported  bool
	Results    []season.Result
}

func parseLatLng(latS, lngS string) (float64, float64, bool) {
	lat, err1 := strconv.ParseFloat(latS, 64)
	lng, err2 := strconv.ParseFloat(lngS, 64)
	if err1 != nil || err2 != nil || math.IsNaN(lat) || math.IsNaN(lng) || math.IsInf(lat, 0) || math.IsInf(lng, 0) ||
		lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return 0, 0, false
	}
	return lat, lng, true
}

// parseDate accepts the cook's local date (YYYY-MM-DD) only if it is within
// one calendar day of the server's current UTC date. Anything else, e.g. a
// date frozen into a bookmarked URL, is rejected so callers fall back to the
// server date (§12.5).
func (s *Server) parseDate(date string) (time.Time, bool) {
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return time.Time{}, false
	}
	y, m, day := s.d.Now().UTC().Date()
	today := time.Date(y, m, day, 0, 0, 0, 0, time.UTC)
	if diff := d.Sub(today); diff < -24*time.Hour || diff > 24*time.Hour {
		return time.Time{}, false
	}
	return d, true
}

// monthFor uses the cook's local date; missing, malformed or stale → server UTC (§12.5).
func (s *Server) monthFor(date string) time.Month {
	if d, ok := s.parseDate(date); ok {
		return d.Month()
	}
	return s.d.Now().UTC().Month()
}

// validDate returns date if parseDate accepts it, else "" (so links omit it).
func (s *Server) validDate(date string) string {
	if _, ok := s.parseDate(date); ok {
		return date
	}
	return ""
}

func coords(lat, lng float64) string {
	ns, ew := "N", "E"
	if lat < 0 {
		ns = "S"
	}
	if lng < 0 {
		ew = "W"
	}
	return fmt.Sprintf("%.2f°%s, %.2f°%s", math.Abs(lat), ns, math.Abs(lng), ew)
}

func ingredientURL(slug string, cell int, date string) string {
	v := url.Values{"cell": {strconv.Itoa(cell)}}
	if date != "" {
		v.Set("date", date)
	}
	return "/ingredient/" + url.PathEscape(slug) + "?" + v.Encode()
}

func (s *Server) season(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	lat, lng, ok := parseLatLng(q.Get("lat"), q.Get("lng"))
	if !ok {
		s.render(w, r, http.StatusBadRequest, "Seasons", "", nil, s.unsupported())
		return
	}
	ctx := r.Context()
	month := s.monthFor(q.Get("date"))
	date := s.validDate(q.Get("date"))
	retry := seasonRetryURL(lat, lng, q.Get("place"), date)
	queryCell := climate.CellID(lat, lng)
	SetCellID(ctx, queryCell)

	// Read the version before the snapshot: the watcher swaps the snapshot
	// before publishing a new version, so this order never caches old
	// snapshot results under a new version key.
	version := s.d.DataVersion()
	snap := s.d.Snapshot()
	if snap == nil {
		s.render(w, r, http.StatusServiceUnavailable, "Seasons", "", nil, views.LoadError(coords(lat, lng), retry))
		return
	}
	key := cache.Key{CellID: queryCell, Month: month, DataVersion: version}
	core, hit := s.cache.Get(key)
	if !hit {
		in, err := s.inputsFor(ctx, queryCell, version)
		if errors.Is(err, errBusy) {
			s.overloaded(w, r, "Seasons", coords(lat, lng), retry)
			return
		}
		if err != nil {
			s.d.Log.Warn("season lookup failed", "err", err, "request_id", RequestIDFrom(ctx), "cell_id", queryCell)
			s.render(w, r, http.StatusServiceUnavailable, "Seasons", "", nil, views.LoadError(coords(lat, lng), retry))
			return
		}
		core = s.compute(ctx, in, month, snap)
		s.cache.Add(key, core)
	}
	if !core.Supported {
		s.render(w, r, http.StatusOK, "Seasons", "", nil, s.unsupported())
		return
	}
	vm := s.seasonVM(ctx, core, snap, month, q.Get("place"), lat, lng, date)
	s.renderPage(w, r, http.StatusOK, vm.Label, views.SeasonDescription(vm.Label, vm.MonthName), "", nil, views.Season(vm))
}

// compute runs the engine on a cell's inputs. It makes no lookups of its
// own, so the result is a function of the cache key.
func (s *Server) compute(ctx context.Context, in cellinput.Inputs, month time.Month, snap *catalog.Snapshot) seasonCore {
	results := s.d.Adjuster.Adjust(ctx, in.NormalsCell, season.Estimate(in.Engine(month), snap))
	return seasonCore{
		QueryCell: in.QueryCell, HasNormals: in.Normals != nil, Supported: in.Supported(), Results: results,
	}
}

// placeLookupTimeout bounds the label's place lookups so a hung places query
// can't eat the whole request budget; on failure the label falls back.
const placeLookupTimeout = 300 * time.Millisecond

// label names the requested point: the ?place= selection if it is within
// nearKm of the point, else the nearest place within nearKm ("near X"), else
// coordinates. It runs per request and is never cached. Both lookups share
// one placeLookupTimeout and one DB gate slot. A full gate, a hung query or
// an outage falls back to coordinates and never fails the request.
func (s *Server) label(ctx context.Context, placeParam string, lat, lng float64) string {
	pctx, cancel := context.WithTimeout(ctx, placeLookupTimeout)
	defer cancel()
	name := ""
	_ = s.withDB(pctx, func(ctx context.Context) error {
		if id, err := strconv.ParseInt(placeParam, 10, 32); err == nil {
			// Only a place near the requested point may label it (a crafted
			// or mismatched URL must not call mid-ocean results "Lisbon").
			if p, ok, err := s.d.Places.ByID(ctx, int32(id)); err == nil && ok && climate.DistanceKm(lat, lng, p.Lat, p.Lng) <= nearKm {
				name = p.Label()
				return nil
			}
		}
		if ctx.Err() != nil { // the shared budget is spent; don't start another query
			return nil
		}
		if p, ok, err := s.d.Places.Nearest(ctx, lat, lng); err == nil && ok && p.DistanceKm <= nearKm {
			name = "near " + p.Label()
		}
		return nil
	})
	if name == "" {
		return coords(lat, lng)
	}
	return name
}

var groupOrder = []struct {
	status season.Status
	title  string
}{
	{season.Peak, "Peak"}, {season.InSeason, "In season"}, {season.FromStorage, "From storage"}, {season.YearRound, "Year-round"},
}

func (s *Server) seasonVM(ctx context.Context, core seasonCore, snap *catalog.Snapshot, month time.Month, placeParam string, lat, lng float64, date string) views.SeasonVM {
	vm := views.SeasonVM{Label: s.label(ctx, placeParam, lat, lng), MonthName: month.String()}
	byStatus := map[season.Status][]views.Item{}
	var seafood []views.Item
	growing := 0
	for _, res := range core.Results {
		ing, ok := snap.Get(res.Slug)
		if !ok || !s.visible(ing) {
			continue
		}
		it := views.Item{
			Name: ing.Name, StatusClass: views.StatusClass(res.Status), Basis: res.Basis,
			URL: ingredientURL(res.Slug, core.QueryCell, date), Estimate: res.Estimate,
		}
		if ing.Kind == catalog.KindSeafood {
			seafood = append(seafood, it)
			continue
		}
		if res.Status == season.Peak || res.Status == season.InSeason {
			growing++
		}
		byStatus[res.Status] = append(byStatus[res.Status], it)
	}
	vm.Harsh = core.HasNormals && growing < harshThreshold
	for _, g := range groupOrder {
		if items := byStatus[g.status]; len(items) > 0 {
			sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
			vm.Groups = append(vm.Groups, views.Group{Title: g.title, Items: items})
		}
	}
	if len(seafood) > 0 {
		sort.Slice(seafood, func(i, j int) bool { return seafood[i].Name < seafood[j].Name })
		vm.Groups = append(vm.Groups, views.Group{Title: "Seafood", Items: seafood})
	}
	return vm
}
