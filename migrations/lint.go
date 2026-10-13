package migrations

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const checksumsName = "checksums"

func verifyChecksums(dir string) error {
	pins, err := readChecksums(filepath.Join(dir, checksumsName))
	if err != nil {
		return err
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("%s: no *.sql files", dir)
	}
	seen := map[string]bool{}
	var errs []string
	for _, path := range files {
		name := filepath.Base(path)
		seen[name] = true
		sum, err := fileSHA256(path)
		if err != nil {
			return err
		}
		want, ok := pins[name]
		if !ok {
			errs = append(errs, fmt.Sprintf("%s is not pinned; add this line to %s:\n%s  %s", name, checksumsName, sum, name))
			continue
		}
		if sum != want {
			errs = append(errs, fmt.Sprintf("%s does not match its pin (do not edit a shipped migration; add a new one)\n  pinned: %s\n  actual: %s", name, want, sum))
		}
	}
	var extra []string
	for name := range pins {
		if !seen[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	for _, name := range extra {
		errs = append(errs, fmt.Sprintf("%s is pinned in %s but the file is missing", name, checksumsName))
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(errs, "\n"))
}

func readChecksums(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for i, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		sum, name, ok := strings.Cut(line, "  ")
		if !ok || len(sum) != 64 || name == "" || strings.Contains(name, " ") {
			return nil, fmt.Errorf("%s:%d: want `<sha256>  <filename>`", path, i+1)
		}
		if _, exists := out[name]; exists {
			return nil, fmt.Errorf("%s:%d: duplicate pin for %s", path, i+1, name)
		}
		out[name] = sum
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no pins", path)
	}
	return out, nil
}

func fileSHA256(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}

var (
	gooseUpRE      = regexp.MustCompile(`(?m)^-- \+goose Up\s*$`)
	gooseDownRE    = regexp.MustCompile(`(?m)^-- \+goose Down\s*$`)
	contractRE     = regexp.MustCompile(`(?i)--\s*contract:`)
	destructiveRE  = regexp.MustCompile(`(?i)\b(?:DROP\s+(?:TABLE|VIEW|INDEX|FUNCTION|TYPE|COLUMN|TRIGGER|EXTENSION|SCHEMA|SEQUENCE|ROLE|DATABASE|CONSTRAINT|MATERIALIZED\s+VIEW)|ALTER\s+TABLE\b[^;\n]*\bDROP\s+COLUMN|RENAME\s+(?:TO|COLUMN))\b`)
	sqlCommentOnly = regexp.MustCompile(`^\s*--`)
)

type contractViolation struct {
	File string
	Line int
	Text string
}

func (v contractViolation) String() string {
	return fmt.Sprintf("%s:%d: %s needs `-- contract:` on this line or the line above", v.File, v.Line, strings.TrimSpace(v.Text))
}

func verifyContracts(dir string) error {
	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		return err
	}
	sort.Strings(files)
	var found []contractViolation
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		found = append(found, contractViolations(filepath.Base(path), string(raw))...)
	}
	if len(found) == 0 {
		return nil
	}
	var b strings.Builder
	for i, v := range found {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(v.String())
	}
	return fmt.Errorf("%s", b.String())
}

func contractViolations(name, src string) []contractViolation {
	up, ok := gooseUp(src)
	if !ok {
		return []contractViolation{{File: name, Line: 1, Text: "missing -- +goose Up"}}
	}
	var out []contractViolation
	lines := strings.Split(up.body, "\n")
	prevContract := false
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		n := up.startLine + i
		if sqlCommentOnly.MatchString(line) {
			prevContract = contractRE.MatchString(line)
			continue
		}
		if destructiveRE.MatchString(line) && !contractRE.MatchString(line) && !prevContract {
			out = append(out, contractViolation{File: name, Line: n, Text: line})
		}
		prevContract = false
	}
	return out
}

type gooseSection struct {
	body      string
	startLine int
}

func gooseUp(src string) (gooseSection, bool) {
	upLoc := gooseUpRE.FindStringIndex(src)
	if upLoc == nil {
		return gooseSection{}, false
	}
	rest := src[upLoc[1]:]
	end := len(rest)
	if downLoc := gooseDownRE.FindStringIndex(rest); downLoc != nil {
		end = downLoc[0]
	}
	body := rest[:end]
	startLine := strings.Count(src[:upLoc[1]], "\n") + 1
	return gooseSection{body: body, startLine: startLine}, true
}
