# Reference Data Source Pins and Transport Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fetch refuses any reference file whose sha256 differs from its pin. Plain HTTP is allowed only for pinned sources, and HTTPS→HTTP redirects are refused. A license review carries over only for identical bytes. Raw files are re-verified before transform, and `fetch`/`approve-license` show what changed compared with the live version.

**Architecture:** `ingest.Source` gains `SHA256` (the expected hex digest) and `Unpinned` (why it can't be pinned). A test requires every default source to have exactly one of the two. `Pipeline.Fetch` compares each download with its pin before any row is written. It also reads a new `bucket:` scheme from the pipeline's blob store, for a reviewed copy of MEOW if the live query isn't byte-stable. A new `internal/ingest/fetch.go` holds `CheckTransport`, `NewHTTPClient` (redirect policy), `hashFile` and `parseChecksums`. `LicenseReviewedBefore` also matches `source_checksum`. `Transform` re-hashes raw files against the version's recorded checksums. `ReviewSummary` prints per-file provenance, and `cmd/ingest` prints it on `fetch` and makes `approve-license --version N` without `--yes` a dry run.

**Tech Stack:** Go 1.26, pgx/v5, sqlc 1.29.0 (`./scripts/sqlc.sh generate`), testcontainers via `internal/dbtest`, `net/http/httptest`.

**Spec:** `docs/superpowers/specs/2026-09-27-reference-data-provenance-design.md`, Part B (§4.4-§4.6, §5 steps 7-13, §6, §7 Part B). Issue [#11](https://github.com/Muddl/thymeandplace/issues/11).

**Scope note:** Part A (F05: the `UNRESOLVED:` license gate, `Attribution()`, the license register) shipped in `620cf2b`. Issue #5 stays open for the owner's MEOW license review (spec §4.2), which is not code. This plan changes nothing about that gate.

## Global Constraints

- Pins belong to content, not URLs. They still apply when `OCEAN_SOURCE_URL`, `OCEAN_LAND_URL` or `CLIMATE_SOURCE_BASE` overrides a URL.
- Pin values (from `docs/calibration-baseline.md:16`), copied exactly:
  - `TerraClimate_19912020_tmin.nc` = `93961a5e423c02282bc1aea2761dbdd28bb8ec1ee79ca571f4b8b1db29d42a7a`
  - `TerraClimate_19912020_tmax.nc` = `04e1293284826d5c9814336a52c389d8552523dbe3ccd3aca072e6ca17ac6f58`
  - `TerraClimate_19912020_ppt.nc` = `eae7eaa3f27c17376178af7a51e6cc5918287d99df35dc00e93ef274b174f211`
  - `ne_50m_land.geojson` = `e874b27a51d146452be360cafb3cc50c86001074a67d534113e6534682f9826b`
  - `meow.geojson` = `533ce94372c46865bc7afe8e2892fb0e1014cc1f8376e5d49679b7da2ab4428c`
- GeoNames (`cities1000.zip`, `admin1CodesASCII.txt`) is unpinned, with the reason "GeoNames regenerates its dumps at fixed, unversioned URLs".
- On a pin mismatch, the fetch error names the file and both hashes, and says "live data unchanged; if the publisher changed this file on purpose, review it and update the pin in internal/ingest/<dataset>.go". No `dataset_versions` row is written and nothing is uploaded.
- `http://` and `bucket:` sources need a pin; `https://` doesn't. Any other scheme is refused.
- Redirects: at most 10, and never from an HTTPS original request to a non-HTTPS URL.
- No migration. The `source_checksum` format stays `sha256:<file>=<hex>;<file>=<hex>`.
- `approve-license` without `--version` still exits 2 before connecting to anything.

## Review Focus

1. **A version fetched before this change** (the same bytes, recorded with the same checksum format) must still count as "reviewed before", so re-fetching the live climate data doesn't force a needless re-approval. Task 4's test re-fetches identical bytes and expects the review to carry over.
2. **A `source_checksum` that is malformed or missing a file**, from an old or hand-edited row. `Transform` must refuse with a clear message and never treat the file as verified. Task 5 covers the missing-entry case.
3. **An env-overridden URL serving different bytes**, such as a test mirror. The fetch must fail the pin, not quietly accept the file. Task 2's mismatch test drives this through an `httptest` URL in place of the default.
4. **A redirect chain that goes HTTPS → HTTPS → HTTP.** It must be refused at the last hop, because the check compares with `via[0]`. Task 3's redirect test includes this chain.
5. **Places refreshes after this change.** GeoNames is unpinned, so every fetch needs `approve-license` (spec §8 question 4, accepted). The summary must say `unpinned: <reason>`, so the operator knows why. Task 6 asserts that text.

---

### Task 1: `Source` provenance fields and default pins

**Files:**
- Modify: `internal/ingest/pipeline.go:25` (`Source`)
- Modify: `internal/ingest/climate.go:32-44`, `internal/ingest/ocean.go:36-52`, `internal/ingest/places.go:28-32`
- Modify: `internal/ingest/climate_test.go:44`, `internal/ingest/places_test.go:60`, `:119-122` (keyed literals)
- Test: `internal/ingest/license_test.go` (`TestDefaultSourcesDeclareProvenance`)

**Interfaces:**
- Produces:
  ```go
  type Source struct {
      URL, File string
      SHA256    string // expected hex sha256; empty only when Unpinned is set
      Unpinned  string // why the content can't be pinned; empty when SHA256 is set
  }
  ```
  Also the default constants `DefaultLandURL` (a commit-pinned raw URL) and `DefaultClimateBase` (unchanged unless Step 5 finds HTTPS works).

- [ ] **Step 1: Make the test literals keyed**

Change the positional `Source{url, file}` literals to keyed ones, for example `{URL: base + "/a_tmin.nc", File: "a_tmin.nc"}`, at `climate_test.go:44`, `places_test.go:60` and `places_test.go:119-122`. Once `Source` has four fields, positional literals stop compiling.

- [ ] **Step 2: Write the failing test**

Append to `internal/ingest/license_test.go`:

```go
var hexSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Spec §4.4: every default source says exactly how its content is trusted.
func TestDefaultSourcesDeclareProvenance(t *testing.T) {
	for _, ds := range []Dataset{NewPlaces(), NewOcean(""), NewClimate("")} {
		for _, s := range ds.Sources() {
			pinned, reason := s.SHA256 != "", strings.TrimSpace(s.Unpinned) != ""
			if pinned == reason {
				t.Errorf("%s/%s: want exactly one of SHA256 or Unpinned (got %q, %q)", ds.Name(), s.File, s.SHA256, s.Unpinned)
			}
			if pinned && !hexSHA256.MatchString(s.SHA256) {
				t.Errorf("%s/%s: SHA256 %q is not 64 lowercase hex characters", ds.Name(), s.File, s.SHA256)
			}
		}
	}
	// Overriding a URL keeps the content pin.
	if o := NewOcean("https://mirror.example/meow.geojson"); o.Source.SHA256 == "" {
		t.Error("NewOcean with a URL override lost the MEOW pin")
	}
	for _, s := range NewClimate("https://mirror.example/clim").Sources() {
		if s.SHA256 == "" {
			t.Errorf("NewClimate with a base override lost the pin for %s", s.File)
		}
	}
	if strings.Contains(DefaultLandURL, "/master/") {
		t.Error("DefaultLandURL must name a commit, not a branch")
	}
}
```

Add `"regexp"` to the imports.

- [ ] **Step 3: Run it and confirm it fails**

Run: `go test ./internal/ingest/ -run TestDefaultSourcesDeclareProvenance -count=1`
Expected: FAIL to compile (`s.SHA256 undefined`).

- [ ] **Step 4: Find the Natural Earth commit whose file matches the baseline pin**

Run:

```bash
for sha in $(gh api "repos/nvkelso/natural-earth-vector/commits?path=geojson/ne_50m_land.geojson&per_page=30" --jq '.[].sha'); do
  sum=$(curl -fsSL "https://raw.githubusercontent.com/nvkelso/natural-earth-vector/$sha/geojson/ne_50m_land.geojson" | sha256sum | cut -d' ' -f1)
  echo "$sha $sum"
done | grep e874b27a51d146452be360cafb3cc50c86001074a67d534113e6534682f9826b
```

Expected: at least one line. Use the **newest** matching commit SHA (the first line). If nothing matches, stop and report. The spec (§4.4) then says to pin the current file and re-run calibration, and that is the owner's call.

- [ ] **Step 5: Check whether THREDDS serves the TerraClimate files over HTTPS (spec §8 question 3)**

Run: `curl -fsSL https://thredds.northwestknowledge.net/thredds/fileServer/TERRACLIMATE_ALL/climatology/TerraClimate_19912020_ppt.nc | sha256sum`
Expected: either `eae7eaa3…f211`, in which case switch `DefaultClimateBase` to `https://thredds.northwestknowledge.net/thredds/fileServer/TERRACLIMATE_ALL/climatology`, or an error or different hash, in which case keep the `http://…:8080` base. It is safe because the files are pinned. Record the outcome in the commit message.

- [ ] **Step 6: Implement the fields and pins**

`internal/ingest/pipeline.go`: replace `type Source struct{ URL, File string }` with the struct from the Interfaces block, plus this doc comment:

```go
// Source is one file a dataset fetches. Its content is trusted either by a
// pin (SHA256, checked on every fetch whatever the URL) or not at all, with
// Unpinned saying why. A test requires exactly one of the two.
```

`internal/ingest/climate.go`: add

```go
// climatePins are the reviewed sha256s of the TerraClimate files
// (docs/calibration-baseline.md). They apply whatever CLIMATE_SOURCE_BASE says.
var climatePins = map[string]string{
	"tmin": "93961a5e423c02282bc1aea2761dbdd28bb8ec1ee79ca571f4b8b1db29d42a7a",
	"tmax": "04e1293284826d5c9814336a52c389d8552523dbe3ccd3aca072e6ca17ac6f58",
	"ppt":  "eae7eaa3f27c17376178af7a51e6cc5918287d99df35dc00e93ef274b174f211",
}
```

and build each source as `Source{URL: strings.TrimRight(base, "/") + "/" + f, File: f, SHA256: climatePins[v]}`.

`internal/ingest/ocean.go`:
- `DefaultLandURL = "https://raw.githubusercontent.com/nvkelso/natural-earth-vector/<SHA from Step 4>/geojson/ne_50m_land.geojson"`, with the comment "pinned to a commit whose file matches the reviewed sha256; never a branch".
- In `NewOcean`, set `SHA256: "533ce94372c46865bc7afe8e2892fb0e1014cc1f8376e5d49679b7da2ab4428c"` on `Source` and `SHA256: "e874b27a51d146452be360cafb3cc50c86001074a67d534113e6534682f9826b"` on `Land`.

`internal/ingest/places.go`: add the constant `const geonamesUnpinned = "GeoNames regenerates its dumps at fixed, unversioned URLs"` and set `Unpinned: geonamesUnpinned` on both sources.

- [ ] **Step 7: Run the ingest tests**

Run: `go test ./internal/ingest/ ./cmd/ingest/ -count=1`
Expected: PASS. Test datasets built with test sources have no pins, and Fetch doesn't check pins yet.

- [ ] **Step 8: Commit**

```bash
git add internal/ingest/
git commit -m "ingest: every source declares a sha256 pin or why it has none (#11)"
```

### Task 2: Fetch checks pins; the `bucket:` source

**Files:**
- Create: `internal/ingest/fetch.go` (`hashFile`, `parseChecksums`, and `(*Pipeline).get`)
- Modify: `internal/ingest/pipeline.go:111-126` (the download loop in `Fetch`)
- Test: `internal/ingest/fetch_test.go`

**Interfaces:**
- Produces:
  - `func hashFile(path string) (string, error)`: hex sha256.
  - `func parseChecksums(s string) (map[string]string, error)`: parses `sha256:a=x;b=y`, and errors on a missing prefix or a malformed entry.
  - `func (p *Pipeline) get(ctx context.Context, src Source, dst string) (string, error)`: a `bucket:<key>` URL reads through `p.Blob.GetFile` and then hashes; anything else goes through `download`.

- [ ] **Step 1: Write the failing tests**

Create `internal/ingest/fetch_test.go`:

```go
package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"thymeandplace/internal/dbtest"
)

func sumOf(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// pinnedPlaces serves files and pins each source to pins[file] ("" = unpinned).
func pinnedPlaces(t *testing.T, files map[string][]byte, pins map[string]string) *Places {
	t.Helper()
	base := serveFiles(t, files)
	var src []Source
	for _, f := range []string{"cities1000.zip", "admin1CodesASCII.txt"} {
		s := Source{URL: base + "/" + f, File: f, SHA256: pins[f]}
		if s.SHA256 == "" {
			s.Unpinned = "test"
		}
		src = append(src, s)
	}
	return &Places{SourceList: src}
}

func TestFetchRejectsPinMismatch(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	files := map[string][]byte{"cities1000.zip": []byte("zip bytes"), "admin1CodesASCII.txt": []byte("changed upstream")}
	p := newPipeline(t, pool, pinnedPlaces(t, files, map[string]string{"admin1CodesASCII.txt": sumOf([]byte("reviewed bytes"))}))

	_, err := p.Fetch(ctx)
	if err == nil || !strings.Contains(err.Error(), "admin1CodesASCII.txt") || !strings.Contains(err.Error(), sumOf([]byte("changed upstream"))) ||
		!strings.Contains(err.Error(), "live data unchanged") {
		t.Fatalf("want a pin mismatch naming the file and both hashes, got %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM dataset_versions`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("dataset_versions rows = %d (%v); want 0", n, err)
	}
	if entries, _ := os.ReadDir(filepath.Join(p.Blob.(FSBlob).Root, "raw")); len(entries) != 0 {
		t.Fatal("a rejected fetch uploaded raw files")
	}
}

func TestFetchAcceptsPin(t *testing.T) {
	pool := dbtest.New(t)
	files := map[string][]byte{"cities1000.zip": []byte("zip bytes"), "admin1CodesASCII.txt": []byte("reviewed bytes")}
	p := newPipeline(t, pool, pinnedPlaces(t, files, map[string]string{"admin1CodesASCII.txt": sumOf([]byte("reviewed bytes"))}))
	if _, err := p.Fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestFetchFromBucketSource(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	p := newPipeline(t, pool, nil)
	body := []byte("a reviewed copy")
	local := filepath.Join(t.TempDir(), "meow.geojson")
	if err := os.WriteFile(local, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.Blob.PutFile(ctx, "pinned/ocean/meow.geojson", local); err != nil {
		t.Fatal(err)
	}
	base := serveFiles(t, map[string][]byte{"admin1CodesASCII.txt": []byte("x")})
	p.DS = &Places{SourceList: []Source{
		{URL: "bucket:pinned/ocean/meow.geojson", File: "cities1000.zip", SHA256: sumOf(body)},
		{URL: base + "/admin1CodesASCII.txt", File: "admin1CodesASCII.txt", Unpinned: "test"},
	}}
	if _, err := p.Fetch(ctx); err != nil {
		t.Fatalf("bucket: source: %v", err)
	}
	p.DS.(*Places).SourceList[0].SHA256 = sumOf([]byte("other"))
	if _, err := p.Fetch(ctx); err == nil {
		t.Fatal("a bucket: source must still be checked against its pin")
	}
}

func TestParseChecksums(t *testing.T) {
	m, err := parseChecksums("sha256:a.zip=aa;b.txt=bb")
	if err != nil || m["a.zip"] != "aa" || m["b.txt"] != "bb" || len(m) != 2 {
		t.Fatalf("%v %v", m, err)
	}
	for _, bad := range []string{"", "md5:a=b", "sha256:a", "sha256:=x"} {
		if _, err := parseChecksums(bad); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}
```

- [ ] **Step 2: Run them and confirm they fail**

Run: `go test ./internal/ingest/ -run 'TestFetchRejectsPinMismatch|TestFetchAcceptsPin|TestFetchFromBucketSource|TestParseChecksums' -count=1`
Expected: FAIL (`undefined: parseChecksums`; the mismatch is accepted today).

- [ ] **Step 3: Implement**

Create `internal/ingest/fetch.go`:

```go
package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// bucketScheme marks a source read from the pipeline's own blob store (a
// reviewed copy, e.g. pinned/ocean/meow.geojson). It must be pinned.
const bucketScheme = "bucket:"

// get fetches src to dst and returns its hex sha256.
func (p *Pipeline) get(ctx context.Context, src Source, dst string) (string, error) {
	if key, ok := strings.CutPrefix(src.URL, bucketScheme); ok {
		if err := p.Blob.GetFile(ctx, key, dst); err != nil {
			return "", err
		}
		return hashFile(dst)
	}
	return download(ctx, p.HTTP, src.URL, dst)
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// parseChecksums reads dataset_versions.source_checksum:
// "sha256:<file>=<hex>;<file>=<hex>".
func parseChecksums(s string) (map[string]string, error) {
	rest, ok := strings.CutPrefix(s, "sha256:")
	if !ok || rest == "" {
		return nil, errors.New("source_checksum is not in sha256:<file>=<hex>;… form")
	}
	out := map[string]string{}
	for _, part := range strings.Split(rest, ";") {
		file, sum, ok := strings.Cut(part, "=")
		if !ok || file == "" || sum == "" {
			return nil, fmt.Errorf("source_checksum entry %q is malformed", part)
		}
		out[file] = sum
	}
	return out, nil
}
```

In `Pipeline.Fetch`, replace the download loop with:

```go
	for _, src := range p.DS.Sources() {
		sum, err := p.get(ctx, src, filepath.Join(incoming, src.File))
		if err != nil {
			return 0, fmt.Errorf("download %s: %w", src.URL, err)
		}
		// The pin is checked before any row is written or file uploaded.
		if src.SHA256 != "" && sum != src.SHA256 {
			return 0, fmt.Errorf("%s: sha256 %s does not match the pin %s; live data unchanged; if the publisher changed this file on purpose, review it and update the pin in internal/ingest/%s.go",
				src.File, sum, src.SHA256, name)
		}
		urls = append(urls, src.URL)
		sums = append(sums, src.File+"="+sum)
	}
```

- [ ] **Step 4: Run the ingest tests**

Run: `go test ./internal/ingest/ -count=1`
Expected: PASS. Existing tests serve unpinned sources, so they are unaffected.

- [ ] **Step 5: Commit**

```bash
git add internal/ingest/
git commit -m "ingest: fetch refuses files that don't match their pin; bucket: sources (#11)"
```

### Task 3: Transport check and redirect policy

**Files:**
- Modify: `internal/ingest/fetch.go` (add `CheckTransport`, `NewHTTPClient`)
- Modify: `cmd/ingest/main.go:106-110` (client) and the step dispatch (transport check)
- Test: `internal/ingest/fetch_test.go` (`TestCheckTransport`, `TestRedirectPolicy`)

**Interfaces:**
- Produces:
  - `func CheckTransport(ds Dataset) error`
  - `func NewHTTPClient(timeout time.Duration) *http.Client`

- [ ] **Step 1: Write the failing tests**

Append to `internal/ingest/fetch_test.go` (add `"net/http"`, `"net/http/httptest"`, `"strconv"`, `"time"`):

```go
func TestCheckTransport(t *testing.T) {
	pin := strings.Repeat("a", 64)
	for _, tc := range []struct {
		src Source
		ok  bool
	}{
		{Source{URL: "https://x/f", File: "f", Unpinned: "r"}, true},
		{Source{URL: "http://x/f", File: "f", SHA256: pin}, true},
		{Source{URL: "bucket:pinned/f", File: "f", SHA256: pin}, true},
		{Source{URL: "http://x/f", File: "f", Unpinned: "r"}, false},
		{Source{URL: "bucket:pinned/f", File: "f", Unpinned: "r"}, false},
		{Source{URL: "ftp://x/f", File: "f", SHA256: pin}, false},
	} {
		err := CheckTransport(&Places{SourceList: []Source{tc.src}})
		if (err == nil) != tc.ok {
			t.Errorf("%s pinned=%v: err=%v, want ok=%v", tc.src.URL, tc.src.SHA256 != "", err, tc.ok)
		}
	}
	for _, ds := range []Dataset{NewPlaces(), NewOcean(""), NewClimate("")} {
		if err := CheckTransport(ds); err != nil {
			t.Errorf("default %s: %v", ds.Name(), err)
		}
	}
}

func TestRedirectPolicy(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer plain.Close()
	var tlsSrv *httptest.Server
	tlsSrv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/to-http":
			http.Redirect(w, r, plain.URL+"/f", http.StatusFound)
		case "/to-https":
			http.Redirect(w, r, tlsSrv.URL+"/final", http.StatusFound)
		case "/hop":
			http.Redirect(w, r, tlsSrv.URL+"/to-http", http.StatusFound) // https → https → http
		case "/loop":
			n, _ := strconv.Atoi(r.URL.Query().Get("n"))
			http.Redirect(w, r, tlsSrv.URL+"/loop?n="+strconv.Itoa(n+1), http.StatusFound)
		default:
			_, _ = w.Write([]byte("ok"))
		}
	}))
	defer tlsSrv.Close()
	c := NewHTTPClient(5 * time.Second)
	c.Transport = tlsSrv.Client().Transport
	get := func(path string) error {
		resp, err := c.Get(tlsSrv.URL + path)
		if err == nil {
			resp.Body.Close()
		}
		return err
	}
	if err := get("/to-http"); err == nil || !strings.Contains(err.Error(), "HTTPS to http") {
		t.Fatalf("https → http must be refused: %v", err)
	}
	if err := get("/hop"); err == nil {
		t.Fatal("https → https → http must be refused")
	}
	if err := get("/to-https"); err != nil {
		t.Fatalf("https → https must be followed: %v", err)
	}
	if err := get("/loop"); err == nil || !strings.Contains(err.Error(), "10 redirects") {
		t.Fatalf("11 redirects must be refused: %v", err)
	}
}
```

- [ ] **Step 2: Run them and confirm they fail**

Run: `go test ./internal/ingest/ -run 'TestCheckTransport|TestRedirectPolicy' -count=1`
Expected: FAIL to compile.

- [ ] **Step 3: Implement**

Append to `internal/ingest/fetch.go` (add `"net/http"`, `"net/url"`, `"time"`):

```go
// CheckTransport refuses sources whose integrity rests on nothing: plain
// HTTP and bucket: are allowed only with a sha256 pin; HTTPS is allowed
// either way. cmd/ingest calls it before any step that fetches.
func CheckTransport(ds Dataset) error {
	for _, s := range ds.Sources() {
		u, err := url.Parse(s.URL)
		if err != nil {
			return fmt.Errorf("%s: bad source URL: %w", s.File, err)
		}
		switch {
		case u.Scheme == "https":
		case (u.Scheme == "http" || u.Scheme+":" == bucketScheme) && s.SHA256 != "":
		case u.Scheme == "http" || u.Scheme+":" == bucketScheme:
			return fmt.Errorf("%s: %s sources must be pinned by sha256", s.File, u.Scheme)
		default:
			return fmt.Errorf("%s: unsupported source scheme %q", s.File, u.Scheme)
		}
	}
	return nil
}

// NewHTTPClient is the fetch client: it follows at most 10 redirects (a
// non-nil CheckRedirect replaces Go's default limit) and never follows one
// from an HTTPS request to a non-HTTPS URL.
func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			if via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
				return fmt.Errorf("refusing redirect from HTTPS to %s", req.URL.Scheme)
			}
			return nil
		},
	}
}
```

In `cmd/ingest/main.go`:
- Replace `HTTP: &http.Client{Timeout: 2 * time.Hour},` with `HTTP: ingest.NewHTTPClient(2 * time.Hour),`, and drop the `net/http` import if it is now unused.
- Right after `p := &ingest.Pipeline{...}`, add:
  ```go
	if step == "fetch" || (step == "all" && v == 0) {
		if err := ingest.CheckTransport(p.DS); err != nil {
			return fail(err)
		}
	}
  ```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/ingest/ ./cmd/ingest/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ingest/ cmd/ingest/
git commit -m "ingest: plain HTTP only for pinned sources; no HTTPS-to-HTTP redirects (#11)"
```

### Task 4: A review carries over only for the same bytes

**Files:**
- Modify: `internal/store/queries/ingest.sql:29-33` (`LicenseReviewedBefore`)
- Regenerate: `internal/store/ingest.sql.go` (`./scripts/sqlc.sh generate`)
- Modify: `internal/ingest/pipeline.go:128-134` (pass the checksum)
- Test: `internal/ingest/swap_test.go` (`TestReviewCarriesOverOnlyForSameBytes`)

**Interfaces:**
- Produces: `store.LicenseReviewedBeforeParams` gains `SourceChecksum string`.

- [ ] **Step 1: Write the failing test**

Append to `internal/ingest/swap_test.go` (it needs `sumOf` and `pinnedPlaces` from `fetch_test.go`, in the same package):

```go
// Spec §4.5: an approval covers exact bytes.
func TestReviewCarriesOverOnlyForSameBytes(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	files := map[string][]byte{"cities1000.zip": []byte("v1 zip"), "admin1CodesASCII.txt": []byte("v1 admin")}
	p := newPipeline(t, pool, pinnedPlaces(t, files, nil))
	reviewed := func(v int64) bool { dv, _ := p.version(ctx, v); return dv.LicenseReviewed }

	v1, err := p.Fetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.ApproveLicense(ctx, v1); err != nil {
		t.Fatal(err)
	}
	files["admin1CodesASCII.txt"] = []byte("v2 admin, changed")
	v2, err := p.Fetch(ctx)
	if err != nil || reviewed(v2) {
		t.Fatalf("changed bytes must not inherit the review: v2=%d reviewed=%v err=%v", v2, reviewed(v2), err)
	}
	files["admin1CodesASCII.txt"] = []byte("v1 admin")
	v3, err := p.Fetch(ctx)
	if err != nil || !reviewed(v3) {
		t.Fatalf("v1's exact bytes again must inherit the review: v3=%d reviewed=%v err=%v", v3, reviewed(v3), err)
	}
}
```

- [ ] **Step 2: Run it and confirm it fails**

Run: `go test ./internal/ingest/ -run TestReviewCarriesOverOnlyForSameBytes -count=1`
Expected: FAIL (`changed bytes must not inherit the review`).

- [ ] **Step 3: Implement**

`internal/store/queries/ingest.sql`:

```sql
-- name: LicenseReviewedBefore :one
-- An approval covers exact bytes: same license, same URLs, same checksums.
SELECT EXISTS (
  SELECT 1 FROM dataset_versions
  WHERE dataset = @dataset AND license = @license AND source_url = @source_url
    AND source_checksum = @source_checksum AND license_reviewed
);
```

Run: `./scripts/sqlc.sh generate`

In `Pipeline.Fetch`, build the checksum string once and pass it to both calls:

```go
	sourceURL := strings.Join(urls, "\n")
	checksum := "sha256:" + strings.Join(sums, ";")
	reviewed, err := p.q().LicenseReviewedBefore(ctx, store.LicenseReviewedBeforeParams{
		Dataset: name, License: p.DS.License(), SourceUrl: sourceURL, SourceChecksum: checksum})
```

and use `SourceChecksum: checksum` in `CreateDatasetVersion`.

- [ ] **Step 4: Run the tests and confirm the generated code is current**

Run: `go test ./internal/ingest/ -count=1 && ./scripts/sqlc.sh generate && git diff --exit-code -- internal/store/`
Expected: PASS. The only diff before staging is the intended one. The existing carry-over assertions in `swap_test.go` still pass, because `placesSource` serves identical bytes every time.

- [ ] **Step 5: Commit**

```bash
git add internal/store/ internal/ingest/
git commit -m "ingest: a license review carries over only for identical bytes (#11)"
```

### Task 5: Verify raw files before transform

**Files:**
- Modify: `internal/ingest/pipeline.go:176-190` (`Transform`)
- Test: `internal/ingest/fetch_test.go` (`TestTransformRejectsTamperedRaw`, `TestTransformRejectsUnrecordedFile`)

**Interfaces:**
- Consumes: `hashFile`, `parseChecksums` (Task 2).
- Produces: `Transform` fails with `raw file <f> differs from what was fetched and reviewed` when a hash doesn't match.

- [ ] **Step 1: Write the failing tests**

Append to `internal/ingest/fetch_test.go`:

```go
func TestTransformRejectsTamperedRaw(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	p := newPipeline(t, pool, placesSource(t, 20))
	v, err := p.Fetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Change the bucket copy and drop the local one, so Transform pulls the
	// changed bytes back.
	root := p.Blob.(FSBlob).Root
	if err := os.WriteFile(filepath.Join(root, "raw", "places", "v"+strconv.FormatInt(v, 10), "admin1CodesASCII.txt"), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(p.WorkDir, "places", "v"+strconv.FormatInt(v, 10), "raw")); err != nil {
		t.Fatal(err)
	}
	if err := p.Transform(ctx, v); err == nil || !strings.Contains(err.Error(), "differs from what was fetched and reviewed") {
		t.Fatalf("want tamper refusal, got %v", err)
	}
}

func TestTransformRejectsUnrecordedFile(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	p := newPipeline(t, pool, placesSource(t, 20))
	v, err := p.Fetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE dataset_versions SET source_checksum = 'sha256:cities1000.zip=00' WHERE id = $1`, v); err != nil {
		t.Fatal(err)
	}
	if err := p.Transform(ctx, v); err == nil {
		t.Fatal("a file with no recorded checksum must not be treated as verified")
	}
}
```

- [ ] **Step 2: Run them and confirm they fail**

Run: `go test ./internal/ingest/ -run 'TestTransformRejects' -count=1`
Expected: FAIL. Transform succeeds today.

- [ ] **Step 3: Implement**

In `Transform`, change `if _, err := p.version(ctx, v); err != nil {` to `dv, err := p.version(ctx, v); if err != nil {`. After the loop that makes the raw files local, add:

```go
	// The bytes about to be transformed must be the ones fetched and
	// reviewed; the bucket copy could have changed since.
	want, err := parseChecksums(dv.SourceChecksum)
	if err != nil {
		return fmt.Errorf("version %d: %w", v, err)
	}
	for _, src := range p.DS.Sources() {
		got, err := hashFile(filepath.Join(dir, "raw", src.File))
		if err != nil {
			return err
		}
		if w, ok := want[src.File]; !ok || got != w {
			return fmt.Errorf("raw file %s differs from what was fetched and reviewed (sha256 %s, recorded %q)", src.File, got, w)
		}
	}
```

- [ ] **Step 4: Run the ingest tests**

Run: `go test ./internal/ingest/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ingest/
git commit -m "ingest: verify raw files against the fetched checksums before transform (#11)"
```

### Task 6: Review summary on `fetch` and `approve-license`

**Files:**
- Create: `internal/ingest/review.go` (`ReviewSummary`)
- Modify: `cmd/ingest/main.go` (`fetch` output, the `approve-license` flow and usage text)
- Test: `internal/ingest/fetch_test.go` (`TestReviewSummary`), `cmd/ingest/main_test.go` (`TestApproveLicenseDryRun`, `TestApproveLicenseNeedsVersionWithoutDB`)

**Interfaces:**
- Produces: `func (p *Pipeline) ReviewSummary(ctx context.Context, v int64) (string, error)`, in this format:
  ```
  license:
    <license>
  files:
    <file>  <url>
      sha256 <hex>  pinned|unpinned: <reason>  same as live vK|changed (live vK: <first 12 hex>…)|no live version
  ```

- [ ] **Step 1: Write the failing tests**

Append to `internal/ingest/fetch_test.go`:

```go
func TestReviewSummary(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	files := map[string][]byte{"cities1000.zip": []byte("zip"), "admin1CodesASCII.txt": []byte("admin")}
	p := newPipeline(t, pool, pinnedPlaces(t, files, nil))
	v1, err := p.Fetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s, err := p.ReviewSummary(ctx, v1)
	if err != nil || !strings.Contains(s, "no live version") || !strings.Contains(s, "unpinned: test") || !strings.Contains(s, p.DS.License()) {
		t.Fatalf("%v\n%s", err, s)
	}
	if _, err := pool.Exec(ctx, `UPDATE dataset_versions SET is_live = true WHERE id = $1`, v1); err != nil {
		t.Fatal(err)
	}
	files["admin1CodesASCII.txt"] = []byte("admin changed")
	v2, err := p.Fetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s, err = p.ReviewSummary(ctx, v2)
	live := "v" + strconv.FormatInt(v1, 10)
	if err != nil || !strings.Contains(s, "same as live "+live) || !strings.Contains(s, "changed (live "+live+": "+sumOf([]byte("admin"))[:12]) {
		t.Fatalf("%v\n%s", err, s)
	}
}
```

Append to `cmd/ingest/main_test.go` (add imports `"context"`, `"net/http"`, `"net/http/httptest"`, `"thymeandplace/internal/dbtest"`):

```go
func TestApproveLicenseNeedsVersionWithoutDB(t *testing.T) {
	ds := map[string]func() ingest.Dataset{"places": func() ingest.Dataset { return ingest.NewPlaces() }}
	var out, errb bytes.Buffer
	if code := run([]string{"places", "approve-license", "--yes"}, func(string) string { return "" }, &out, &errb, ds); code != 2 {
		t.Fatalf("code = %d, stderr = %q", code, errb.String())
	}
}

// approve-license --version N without --yes shows the summary and changes
// nothing; with --yes it approves.
func TestApproveLicenseDryRun(t *testing.T) {
	pool := dbtest.New(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(r.URL.Path)) }))
	defer srv.Close()
	ds := map[string]func() ingest.Dataset{"places": func() ingest.Dataset {
		return &ingest.Places{SourceList: []ingest.Source{
			{URL: srv.URL + "/cities1000.zip", File: "cities1000.zip", Unpinned: "test"},
			{URL: srv.URL + "/admin1CodesASCII.txt", File: "admin1CodesASCII.txt", Unpinned: "test"},
		}}
	}}
	env := map[string]string{"DATABASE_URL": dbtest.URLAs(pool, dbtest.Owner), "BLOB_DIR": t.TempDir(), "INGEST_WORKDIR": t.TempDir()}
	getenv := func(k string) string { return env[k] }
	var out, errb bytes.Buffer
	if code := run([]string{"places", "fetch"}, getenv, &out, &errb, ds); code != 0 {
		t.Fatalf("fetch: %d %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "unpinned: test") {
		t.Fatalf("fetch must print the review summary:\n%s", out.String())
	}
	out.Reset()
	errb.Reset()
	if code := run([]string{"places", "approve-license", "--version", "1"}, getenv, &out, &errb, ds); code != 2 ||
		!strings.Contains(out.String(), "sha256") || !strings.Contains(errb.String(), "re-run with --yes") {
		t.Fatalf("dry run: code %d\n%s\n%s", code, out.String(), errb.String())
	}
	var reviewed bool
	if err := pool.QueryRow(context.Background(), `SELECT license_reviewed FROM dataset_versions WHERE id = 1`).Scan(&reviewed); err != nil || reviewed {
		t.Fatalf("a dry run must not approve (reviewed=%v, %v)", reviewed, err)
	}
	if code := run([]string{"places", "approve-license", "--version", "1", "--yes"}, getenv, &out, &errb, ds); code != 0 {
		t.Fatalf("approve: %d %s", code, errb.String())
	}
}
```

If the first `dataset_versions` id in a fresh `dbtest` database isn't 1, parse the version from the `fetched places version N` line instead.

- [ ] **Step 2: Run them and confirm they fail**

Run: `go test ./internal/ingest/ ./cmd/ingest/ -run 'TestReviewSummary|TestApproveLicense' -count=1`
Expected: FAIL (`undefined: ReviewSummary`; the dry run exits 2 without connecting and prints no summary).

- [ ] **Step 3: Implement `ReviewSummary`**

Create `internal/ingest/review.go`:

```go
package ingest

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// ReviewSummary is what an operator reads before approving version v: the
// license, and for each file its URL, sha256, how it is trusted, and how it
// compares with the live version.
func (p *Pipeline) ReviewSummary(ctx context.Context, v int64) (string, error) {
	dv, err := p.version(ctx, v)
	if err != nil {
		return "", err
	}
	sums, err := parseChecksums(dv.SourceChecksum)
	if err != nil {
		return "", err
	}
	var liveID int64
	var liveSums map[string]string
	live, err := p.q().LiveDatasetVersion(ctx, p.DS.Name())
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return "", err
	default:
		liveID = live.ID
		if liveSums, err = parseChecksums(live.SourceChecksum); err != nil {
			return "", fmt.Errorf("live version %d: %w", live.ID, err)
		}
	}
	urls := strings.Split(dv.SourceUrl, "\n")
	var b strings.Builder
	fmt.Fprintf(&b, "license:\n  %s\nfiles:\n", dv.License)
	for i, src := range p.DS.Sources() {
		u := src.URL
		if i < len(urls) {
			u = urls[i]
		}
		trust := "pinned"
		if src.SHA256 == "" {
			trust = "unpinned: " + src.Unpinned
		}
		cmp := "no live version"
		if liveSums != nil {
			switch l := liveSums[src.File]; {
			case l == sums[src.File]:
				cmp = fmt.Sprintf("same as live v%d", liveID)
			case len(l) >= 12:
				cmp = fmt.Sprintf("changed (live v%d: %s…)", liveID, l[:12])
			default:
				cmp = fmt.Sprintf("changed (live v%d has no record of this file)", liveID)
			}
		}
		fmt.Fprintf(&b, "  %s  %s\n    sha256 %s  %s  %s\n", src.File, u, sums[src.File], trust, cmp)
	}
	return b.String(), nil
}
```

- [ ] **Step 4: Change the CLI**

In `cmd/ingest/main.go`:
- Update the doc comment and `usage` line to `ingest <dataset> approve-license --version N [--yes]`.
- Replace the early check with:
  ```go
	if step == "approve-license" && v == 0 {
		fmt.Fprintln(stderr, "approve-license needs --version N (then --yes after you have read the summary)")
		return 2
	}
  ```
- In `case "fetch":`, replace the license and `source:` lines with:
  ```go
			fmt.Fprintf(stdout, "fetched %s version %d\n", name, v)
			if sum, serr := p.ReviewSummary(ctx, v); serr == nil {
				fmt.Fprint(stdout, sum)
			} else {
				err = serr
			}
  ```
  Keep the two trailing guidance lines (unresolved license, or the `approve-license` hint, now `--version %d` followed by "then --yes").
- Replace `case "approve-license":` with:
  ```go
	case "approve-license":
		sum, serr := p.ReviewSummary(ctx, v)
		if serr != nil {
			err = serr
			break
		}
		fmt.Fprint(stdout, sum)
		if !*yes {
			fmt.Fprintf(stderr, "nothing approved: re-run with --yes to approve %s version %d\n", name, v)
			return 2
		}
		if _, err = p.ApproveLicense(ctx, v); err == nil {
			fmt.Fprintf(stdout, "approved license for %s version %d\n", name, v)
		}
  ```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/ingest/ ./cmd/ingest/ -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/ingest/ cmd/ingest/
git commit -m "ingest: show per-file provenance on fetch; approve-license dry run without --yes (#11)"
```

### Task 7: MEOW byte stability, the pins checked for real, and docs

**Files:**
- Possibly modify: `internal/ingest/ocean.go` (switch MEOW to `bucket:pinned/ocean/meow.geojson`)
- Modify: `deploy/env/ingest.env.example` (a note that URL overrides keep the content pin)
- Modify: `docs/operations.md` (a new "When a pinned source changes" section; update the fetch/approve steps)
- Modify: `docs/superpowers/specs/2026-09-24-thymeandplace-core-engine-design.md` §7, §13.6, §16

**Interfaces:** configuration and documentation only, unless Step 2 changes the MEOW source.

- [ ] **Step 1: Check every pinned default URL by hand (CI has no network)**

Run, with each `<…>` replaced by the constant's value from the Go source:

```bash
curl -fsSL "<DefaultLandURL from ocean.go>" | sha256sum
curl -fsSL "<DefaultOceanURL from ocean.go>" | sha256sum
for v in tmin tmax ppt; do curl -fsSL "<DefaultClimateBase>/TerraClimate_19912020_$v.nc" | sha256sum; done
```

Replace the placeholders with the constants' values before running (don't commit a helper). Expected: each hash equals its pin in the Global Constraints. The three NetCDF files are about 3 GB in total; run this on the VPS or a fast connection.

- [ ] **Step 2: MEOW byte stability (spec §4.4)**

Fetch `DefaultOceanURL` twice, **at least a day apart**, and compare the sha256s.
- **Same hash (and equal to `533ce943…`):** keep the ArcGIS URL with its pin. Nothing changes.
- **Different hashes:** copy the reviewed raw file from production (`raw/ocean/v<live>/meow.geojson`) to `pinned/ocean/meow.geojson` in the bucket (`mc cp`). Change `NewOcean`'s default to `Source{URL: "bucket:pinned/ocean/meow.geojson", File: "meow.geojson", SHA256: "533ce943…"}`, keeping `OCEAN_SOURCE_URL` as the override. Rerun `go test ./internal/ingest/ -count=1`. Commit: `ingest: read MEOW from a reviewed bucket copy; the live query isn't byte-stable (#11)`.

Seafood is off (`FEATURE_SEAFOOD`) and ocean can't be approved until #5 is resolved, so this step doesn't block the rest of the plan. If a day can't pass before merging, open a follow-up issue for Step 2 and link it in the pull request.

- [ ] **Step 3: `deploy/env/ingest.env.example`**

Next to `OCEAN_SOURCE_URL`, `OCEAN_LAND_URL` and `CLIMATE_SOURCE_BASE`, add: `# Overrides change where a file comes from, not what it must contain: the sha256 pin in internal/ingest still applies.`

- [ ] **Step 4: `docs/operations.md`**

- In the fetch → approve → all steps:
  - `fetch` prints a per-file summary.
  - `approve-license --version N` without `--yes` repeats it and changes nothing.
  - Places (unpinned GeoNames) needs `approve-license` on every refresh.
- New section, "When a pinned source changes":
  1. The fetch fails and live data is untouched.
  2. Download the file by hand and compare it with the archived `raw/<dataset>/v<live>/` copy in the bucket.
  3. If the change is legitimate, update the pin in a commit that says why.
  4. Re-run calibration, then fetch, approve and swap.

- [ ] **Step 5: Core spec**

- §7: fetch checks source pins and transport; transform checks raw files; a review carries over only for identical bytes.
- §13.6: the new pipeline tests from spec §6.
- §16: new rows "Source pins: sha256 per file (GeoNames unpinned)" and "Plain HTTP: only for pinned sources; no HTTPS→HTTP redirects".

- [ ] **Step 6: Commit**

```bash
git add deploy/env/ingest.env.example docs/
git commit -m "docs: source pins, transport rules and what to do when a pinned file changes (#11)"
```

### Task 8: Whole-repo check

- [ ] **Step 1:** Run `go vet ./... && go test ./... -count=1`. Expected: PASS.
- [ ] **Step 2:** Run `./scripts/sqlc.sh generate && go tool templ generate && git diff --exit-code`. Expected: no diff.
