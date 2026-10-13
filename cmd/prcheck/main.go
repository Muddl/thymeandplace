// Command prcheck validates a GitHub pull request against docs/WAYS-OF-WORKING.md.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"thymeandplace/internal/prcheck"
)

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr, http.DefaultClient))
}

func run(args []string, getenv func(string) string, stdout, stderr io.Writer, client *http.Client) int {
	mapPath := ".github/pr-signoffs.json"
	for i := 0; i < len(args); i++ {
		if args[i] == "-map" && i+1 < len(args) {
			mapPath = args[i+1]
			i++
			continue
		}
		fmt.Fprintf(stderr, "usage: prcheck [-map .github/pr-signoffs.json]\n")
		return 2
	}

	f, err := os.Open(mapPath)
	if err != nil {
		fmt.Fprintf(stderr, "prcheck: %v\n", err)
		return 2
	}
	cfg, err := prcheck.LoadConfig(f)
	_ = f.Close()
	if err != nil {
		fmt.Fprintf(stderr, "prcheck: %v\n", err)
		return 2
	}

	eventPath := getenv("GITHUB_EVENT_PATH")
	if eventPath == "" {
		fmt.Fprintln(stderr, "prcheck: GITHUB_EVENT_PATH is required")
		return 2
	}
	raw, err := os.ReadFile(eventPath)
	if err != nil {
		fmt.Fprintf(stderr, "prcheck: %v\n", err)
		return 2
	}
	n, err := prNumber(raw)
	if err != nil {
		fmt.Fprintf(stderr, "prcheck: %v\n", err)
		return 2
	}

	repo := getenv("GITHUB_REPOSITORY")
	token := getenv("GITHUB_TOKEN")
	api := getenv("GITHUB_API_URL")
	if api == "" {
		api = "https://api.github.com"
	}
	if repo == "" || token == "" {
		fmt.Fprintln(stderr, "prcheck: GITHUB_REPOSITORY and GITHUB_TOKEN are required")
		return 2
	}

	gh := gitHub{base: strings.TrimRight(api, "/"), repo: repo, token: token, client: client}
	in, err := gh.load(n)
	if err != nil {
		fmt.Fprintf(stderr, "prcheck: %v\n", err)
		return 2
	}
	failures := prcheck.Check(in, cfg)
	if len(failures) == 0 {
		fmt.Fprintf(stdout, "prcheck: ok (#%d %s)\n", n, in.HeadSHA)
		return 0
	}
	for _, msg := range failures {
		fmt.Fprintf(stdout, "prcheck: %s\n", msg)
	}
	return 1
}

type eventLite struct {
	PullRequest *struct {
		Number int `json:"number"`
	} `json:"pull_request"`
	Issue *struct {
		Number      int             `json:"number"`
		PullRequest json.RawMessage `json:"pull_request"`
	} `json:"issue"`
}

func prNumber(raw []byte) (int, error) {
	var ev eventLite
	if err := json.Unmarshal(raw, &ev); err != nil {
		return 0, err
	}
	if ev.PullRequest != nil && ev.PullRequest.Number > 0 {
		return ev.PullRequest.Number, nil
	}
	if ev.Issue != nil && ev.Issue.Number > 0 && len(ev.Issue.PullRequest) > 0 && string(ev.Issue.PullRequest) != "null" {
		return ev.Issue.Number, nil
	}
	return 0, fmt.Errorf("event is not a pull request")
}

type gitHub struct {
	base, repo, token string
	client            *http.Client
}

func (g gitHub) load(n int) (prcheck.Input, error) {
	var pr struct {
		Number int    `json:"number"`
		Body   string `json:"body"`
		Head   struct {
			SHA string `json:"sha"`
		} `json:"head"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := g.get("/repos/"+g.repo+"/pulls/"+strconv.Itoa(n), &pr); err != nil {
		return prcheck.Input{}, err
	}
	var files []struct {
		Filename string `json:"filename"`
	}
	if err := g.getList("/repos/"+g.repo+"/pulls/"+strconv.Itoa(n)+"/files?per_page=100", &files); err != nil {
		return prcheck.Input{}, err
	}
	var comments []struct {
		Body string `json:"body"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if err := g.getList("/repos/"+g.repo+"/issues/"+strconv.Itoa(n)+"/comments?per_page=100", &comments); err != nil {
		return prcheck.Input{}, err
	}

	in := prcheck.Input{
		Body:    pr.Body,
		HeadSHA: pr.Head.SHA,
		Author:  pr.User.Login,
		Issues:  map[int]string{},
	}
	for _, l := range pr.Labels {
		in.Labels = append(in.Labels, l.Name)
	}
	for _, f := range files {
		in.Files = append(in.Files, f.Filename)
	}
	for _, c := range comments {
		in.Comments = append(in.Comments, prcheck.Comment{Body: c.Body, Author: c.User.Login})
	}
	for _, num := range prcheck.ParseIssueLinks(in.Body) {
		body, ok, err := g.getIssue(num)
		if err != nil {
			return prcheck.Input{}, err
		}
		if ok {
			in.Issues[num] = body
		}
	}
	return in, nil
}

func (g gitHub) get(path string, dest any) error {
	body, code, _, err := g.do("GET", g.base+path)
	if err != nil {
		return err
	}
	if code >= 300 {
		return fmt.Errorf("GET %s: HTTP %d: %s", path, code, truncate(body, 200))
	}
	return json.Unmarshal(body, dest)
}

func (g gitHub) getIssue(num int) (string, bool, error) {
	raw, code, _, err := g.do("GET", g.base+"/repos/"+g.repo+"/issues/"+strconv.Itoa(num))
	if err != nil {
		return "", false, err
	}
	if code == 404 || code == 403 {
		return "", false, nil
	}
	if code >= 300 {
		return "", false, fmt.Errorf("GET issue #%d: HTTP %d: %s", num, code, truncate(raw, 200))
	}
	var issue struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal(raw, &issue); err != nil {
		return "", false, err
	}
	return issue.Body, true, nil
}

func (g gitHub) getList(path string, dest any) error {
	url := g.base + path
	var raw []json.RawMessage
	for url != "" {
		body, code, next, err := g.do("GET", url)
		if err != nil {
			return err
		}
		if code >= 300 {
			return fmt.Errorf("GET %s: HTTP %d: %s", url, code, truncate(body, 200))
		}
		var page []json.RawMessage
		if err := json.Unmarshal(body, &page); err != nil {
			return err
		}
		raw = append(raw, page...)
		url = next
	}
	joined, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(joined, dest)
}

func (g gitHub) do(method, url string) ([]byte, int, string, error) {
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return nil, 0, "", err
	}
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "thymeandplace-prcheck")
	res, err := g.client.Do(req)
	if err != nil {
		return nil, 0, "", err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, 0, "", err
	}
	return body, res.StatusCode, nextLink(res.Header.Get("Link")), nil
}

func nextLink(link string) string {
	for _, part := range strings.Split(link, ",") {
		part = strings.TrimSpace(part)
		if strings.HasSuffix(part, `rel="next"`) {
			if i := strings.Index(part, "<"); i >= 0 {
				if j := strings.Index(part, ">"); j > i {
					return part[i+1 : j]
				}
			}
		}
	}
	return ""
}

func truncate(b []byte, n int) string {
	s := strings.TrimSpace(string(b))
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
