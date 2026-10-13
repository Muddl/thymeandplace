// Package prcheck validates pull request issue links and path sign-offs.
package prcheck

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

const (
	noIssueLabel      = "no-issue"
	minSHAPrefix      = 7
	acceptanceHeading = "## Acceptance criteria"
)

var (
	issueLinkRE = regexp.MustCompile(`(?i)\b(?:closes|fixes|refs):?\s+#(\d+)`)
	approvalRE  = regexp.MustCompile(`(?i)^([A-Za-z][A-Za-z ]*) Farmer: approved ([0-9a-f]{7,40})$`)
	dependabot  = regexp.MustCompile(`(?i)^dependabot(\[bot\])?$`)
)

// Config is the path-to-sign-off map loaded from pr-signoffs.json.
type Config struct {
	TrustedAuthors []string          `json:"trustedAuthors"`
	Roles          map[string]string `json:"roles"`
	Rules          []Rule            `json:"rules"`
}

// Rule is one path or label trigger and the labels that satisfy it.
type Rule struct {
	ID        string   `json:"id"`
	Require   []string `json:"require"`
	Paths     []string `json:"paths"`
	Exclude   []string `json:"exclude"`
	WhenLabel string   `json:"whenLabel"`
}

// Comment is one pull request issue comment.
type Comment struct {
	Body   string
	Author string
}

// Input is one pull request snapshot. Issues maps a linked number to its body.
type Input struct {
	Body     string
	HeadSHA  string
	Labels   []string
	Files    []string
	Comments []Comment
	Author   string
	Issues   map[int]string
}

// LoadConfig reads a pr-signoffs.json document.
func LoadConfig(r io.Reader) (Config, error) {
	var cfg Config
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, err
	}
	if len(cfg.Roles) == 0 {
		return Config{}, fmt.Errorf("pr-signoffs: roles is empty")
	}
	if len(cfg.TrustedAuthors) == 0 {
		cfg.TrustedAuthors = []string{"Muddl"}
	}
	for i, rule := range cfg.Rules {
		if rule.ID == "" {
			return Config{}, fmt.Errorf("pr-signoffs: rule %d has no id", i)
		}
		if len(rule.Require) == 0 {
			return Config{}, fmt.Errorf("pr-signoffs: rule %q has no require labels", rule.ID)
		}
		for _, label := range rule.Require {
			if cfg.Roles[label] == "" {
				return Config{}, fmt.Errorf("pr-signoffs: rule %q require %q has no roles entry", rule.ID, label)
			}
		}
	}
	return cfg, nil
}

// Check reports governance failures for in. An empty slice means the pull request passes.
func Check(in Input, cfg Config) []string {
	var failures []string
	if !skipIssueLink(in) {
		failures = append(failures, checkIssueLink(in)...)
	}
	failures = append(failures, checkSignoffs(in, cfg)...)
	return failures
}

func skipIssueLink(in Input) bool {
	if hasLabel(in.Labels, noIssueLabel) {
		return true
	}
	return dependabot.MatchString(strings.TrimSpace(in.Author))
}

func checkIssueLink(in Input) []string {
	nums := ParseIssueLinks(in.Body)
	if len(nums) == 0 {
		return []string{"PR must link an issue with Closes #N, Fixes #N, or Refs #N"}
	}
	var failures []string
	seen := map[int]bool{}
	for _, n := range nums {
		if seen[n] {
			continue
		}
		seen[n] = true
		body, ok := in.Issues[n]
		if !ok {
			failures = append(failures, fmt.Sprintf("linked issue #%d could not be read", n))
			continue
		}
		if !hasAcceptanceHeading(body) {
			failures = append(failures, fmt.Sprintf("issue #%d is missing a %q heading", n, acceptanceHeading))
		}
	}
	return failures
}

// ParseIssueLinks returns issue numbers from Closes, Fixes, or Refs lines.
func ParseIssueLinks(body string) []int {
	matches := issueLinkRE.FindAllStringSubmatch(body, -1)
	var nums []int
	for _, m := range matches {
		var n int
		if _, err := fmt.Sscan(m[1], &n); err != nil || n <= 0 {
			continue
		}
		nums = append(nums, n)
	}
	return nums
}

func hasAcceptanceHeading(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == acceptanceHeading {
			return true
		}
	}
	return false
}

func checkSignoffs(in Input, cfg Config) []string {
	approvals := parseApprovals(in.Comments, cfg.TrustedAuthors)
	labels := map[string]bool{}
	for _, l := range in.Labels {
		labels[l] = true
	}
	needed := requiredLabels(in, cfg)
	var keys []string
	for label := range needed {
		keys = append(keys, label)
	}
	sort.Strings(keys)
	var failures []string
	for _, group := range keys {
		alts := needed[group]
		if satisfied(alts, labels, approvals, in.HeadSHA, cfg.Roles) {
			continue
		}
		failures = append(failures, signoffFailure(alts, labels, approvals, in.HeadSHA, cfg.Roles, neededFiles(in, cfg, alts)))
	}
	return failures
}

func requiredLabels(in Input, cfg Config) map[string][]string {
	needed := map[string][]string{}
	for _, rule := range cfg.Rules {
		if !ruleApplies(rule, in) {
			continue
		}
		key := strings.Join(append([]string(nil), rule.Require...), "|")
		if _, ok := needed[key]; !ok {
			needed[key] = append([]string(nil), rule.Require...)
		}
	}
	return needed
}

func neededFiles(in Input, cfg Config, alts []string) []string {
	var files []string
	seen := map[string]bool{}
	for _, rule := range cfg.Rules {
		if !sameStrings(rule.Require, alts) || !ruleApplies(rule, in) {
			continue
		}
		if rule.WhenLabel != "" {
			continue
		}
		for _, f := range in.Files {
			if seen[f] {
				continue
			}
			if pathMatchesRule(f, rule) {
				seen[f] = true
				files = append(files, f)
			}
		}
	}
	sort.Strings(files)
	return files
}

func ruleApplies(rule Rule, in Input) bool {
	if rule.WhenLabel != "" {
		return hasLabel(in.Labels, rule.WhenLabel)
	}
	for _, f := range in.Files {
		if pathMatchesRule(f, rule) {
			return true
		}
	}
	return false
}

func pathMatchesRule(file string, rule Rule) bool {
	if len(rule.Exclude) > 0 && matchAny(file, rule.Exclude) {
		return false
	}
	return matchAny(file, rule.Paths)
}

func matchAny(file string, patterns []string) bool {
	for _, p := range patterns {
		if MatchPath(p, file) {
			return true
		}
	}
	return false
}

func satisfied(alts []string, labels map[string]bool, approvals map[string]string, head string, roles map[string]string) bool {
	for _, label := range alts {
		if !labels[label] {
			continue
		}
		role := roles[label]
		sha, ok := approvals[strings.ToLower(role)]
		if ok && shaPrefixMatch(sha, head) {
			return true
		}
	}
	return false
}

func signoffFailure(alts []string, labels map[string]bool, approvals map[string]string, head string, roles map[string]string, files []string) string {
	want := strings.Join(quoted(alts), " or ")
	where := ""
	if len(files) > 0 {
		where = " for " + strings.Join(files, ", ")
	}
	headPrefix := shortSHA(head)
	for _, label := range alts {
		if !labels[label] {
			continue
		}
		role := roles[label]
		sha, ok := approvals[strings.ToLower(role)]
		if !ok {
			return fmt.Sprintf("%s is required%s; label is present but no %q Farmer approval matches %s", want, where, role, headPrefix)
		}
		if !shaPrefixMatch(sha, head) {
			return fmt.Sprintf("%s is stale%s; %q Farmer approved %s, head is %s", want, where, role, sha, headPrefix)
		}
	}
	return fmt.Sprintf("%s is required%s on %s", want, where, headPrefix)
}

func quoted(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = "`" + s + "`"
	}
	return out
}

func parseApprovals(comments []Comment, trusted []string) map[string]string {
	out := map[string]string{}
	for _, c := range comments {
		if !trustedAuthor(c.Author, trusted) {
			continue
		}
		for _, line := range strings.Split(c.Body, "\n") {
			trim := strings.TrimSpace(line)
			if strings.HasPrefix(trim, ">") {
				continue
			}
			m := approvalRE.FindStringSubmatch(trim)
			if m == nil {
				continue
			}
			role := strings.ToLower(strings.TrimSpace(m[1]))
			out[role] = strings.ToLower(m[2])
		}
	}
	return out
}

func trustedAuthor(login string, trusted []string) bool {
	login = strings.TrimSpace(login)
	for _, want := range trusted {
		if strings.EqualFold(login, want) {
			return true
		}
	}
	return false
}

func shaPrefixMatch(commentSHA, head string) bool {
	c := strings.ToLower(strings.TrimSpace(commentSHA))
	h := strings.ToLower(strings.TrimSpace(head))
	if len(c) < minSHAPrefix || len(h) < minSHAPrefix || len(c) > len(h) {
		return false
	}
	return strings.HasPrefix(h, c)
}

func shortSHA(head string) string {
	h := strings.ToLower(strings.TrimSpace(head))
	if len(h) > 7 {
		return h[:7]
	}
	return h
}

func hasLabel(labels []string, want string) bool {
	for _, l := range labels {
		if l == want {
			return true
		}
	}
	return false
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
