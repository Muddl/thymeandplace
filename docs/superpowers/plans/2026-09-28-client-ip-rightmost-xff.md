# Client IP from the Rightmost X-Forwarded-For Entry Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Take the client IP from the `X-Forwarded-For` entry Caddy wrote (the rightmost one), never from an entry the client sent, and write the one-hop trust rule down where an operator would change it.

**Architecture:** `web.ClientIP` keeps its signature. With `trustProxy`, it reads every header line, takes the last entry of the last line, and returns it in canonical form if it parses as an IP. Otherwise it falls back to the `RemoteAddr` host. The public limiter and the admin budget both call `ClientIP`, so both pick up the change with no other code edits.

**Tech Stack:** Go 1.26, `net/netip`, `net/http/httptest`.

**Spec:** `docs/superpowers/specs/2026-09-27-admin-abuse-resistance-design.md` (§4.1, §4.6 §14 bullet, §6 `ClientIP` table, §7 operations text). Issue [#10](https://github.com/Muddl/thymeandplace/issues/10).

**Scope note:** The spec's other parts (F04 bounded audit rows, request limiter, daily prune; F09 `/64` budget key) already shipped in `620cf2b`. `LimiterKey` is exported and `auth.Caller` exists. This plan covers only F10.

## Global Constraints

- The app trusts exactly one proxy hop: Caddy.
- `ClientIP(r *http.Request, trustProxy bool) string` keeps its signature.
- Read all header lines with `r.Header.Values("X-Forwarded-For")`, never `r.Header.Get`.
- If the rightmost entry doesn't parse as an IP, fall back to the `RemoteAddr` host. That fails closed, not open.
- Return `addr.WithZone("").String()`, which is the canonical form: `2001:DB8::1` → `2001:db8::1`.
- The existing rule "never publish port 8080" stays and is still required.
- Don't add a `TRUST_PROXY_HOPS` setting or `X-Client-IP` header. Spec §8 question 3 defers that until a CDN is actually chosen.

## Review Focus

1. **Several `X-Forwarded-For` header lines.** A client can send its own header line, and Caddy then adds another line or overwrites the header. The last entry of the *last line* must win. Task 1's table pins this.
2. **A garbled rightmost entry**, such as `"198.51.100.1, garbage"`. This must give the `RemoteAddr` host, never the leftmost entry. Task 1's table pins this.
3. **An empty trailing entry**, such as `"203.0.113.9, "`. The rightmost entry is empty and doesn't parse, so the result is the `RemoteAddr` host. Task 1's table pins this.
4. **An IPv6 zone or uppercase hex** (`fe80::1%eth0`, `2001:DB8::1`). The result must be canonical and zone-free, so `LimiterKey` buckets it the same way every time. Task 1's table pins this.
5. **`RemoteAddr` without a port.** The existing fallback returns `RemoteAddr` as-is. Task 1's table keeps a row so the refactor doesn't break it.

---

### Task 1: Rightmost-entry `ClientIP`

**Files:**
- Modify: `internal/web/middleware.go:145-158` (`ClientIP` and its doc comment)
- Test: `internal/web/middleware_test.go:86-93` (replace `TestClientIP`)

**Interfaces:**
- Consumes: nothing new.
- Produces: `func ClientIP(r *http.Request, trustProxy bool) string` (signature unchanged; behaviour: rightmost entry, canonical form, `RemoteAddr` fallback).

- [ ] **Step 1: Replace `TestClientIP` with a table-driven test**

Replace lines 86-93 of `internal/web/middleware_test.go` with:

```go
// ClientIP trusts exactly one proxy hop (Caddy): the rightmost
// X-Forwarded-For entry is the peer Caddy saw. Anything a client sent sits to
// its left and is ignored. A garbled rightmost entry falls back to RemoteAddr.
func TestClientIP(t *testing.T) {
	for _, tc := range []struct {
		name       string
		remoteAddr string
		xff        []string // one element per header line
		trust      bool
		want       string
	}{
		{"untrusted ignores the header", "10.0.0.5:5555", []string{"203.0.113.9"}, false, "10.0.0.5"},
		{"rightmost entry wins", "10.0.0.5:5555", []string{"198.51.100.1, 203.0.113.9"}, true, "203.0.113.9"},
		{"last entry of the last line", "10.0.0.5:5555", []string{"198.51.100.1", "192.0.2.7, 203.0.113.9"}, true, "203.0.113.9"},
		{"garbled rightmost falls back", "10.0.0.5:5555", []string{"198.51.100.1, garbage"}, true, "10.0.0.5"},
		{"empty rightmost falls back", "10.0.0.5:5555", []string{"203.0.113.9, "}, true, "10.0.0.5"},
		{"no header falls back", "10.0.0.5:5555", nil, true, "10.0.0.5"},
		{"IPv6 canonical form", "10.0.0.5:5555", []string{"2001:DB8::1"}, true, "2001:db8::1"},
		{"IPv6 zone dropped", "10.0.0.5:5555", []string{"fe80::1%eth0"}, true, "fe80::1"},
		{"RemoteAddr without port", "10.0.0.5", nil, true, "10.0.0.5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tc.remoteAddr
			for _, v := range tc.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			if got := ClientIP(r, tc.trust); got != tc.want {
				t.Fatalf("ClientIP = %q, want %q", got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test and confirm it fails**

Run: `go test ./internal/web/ -run TestClientIP -count=1 -v`
Expected: FAIL. "rightmost entry wins" gets `198.51.100.1`, "garbled rightmost falls back" gets `198.51.100.1`, and "IPv6 canonical form" gets `2001:DB8::1`.

- [ ] **Step 3: Implement the rightmost-entry rule**

Replace `ClientIP` and its comment in `internal/web/middleware.go` (currently lines 145-158) with:

```go
// ClientIP returns the caller's IP. The app trusts exactly one proxy hop,
// Caddy: with trustProxy, the rightmost X-Forwarded-For entry (the last entry
// of the last header line) is the peer Caddy saw, and anything a client sent
// sits to its left and is ignored. If that entry doesn't parse as an IP, or
// there is no header, the RemoteAddr host is used. Behind Caddy that is
// Caddy's own address, a bucket shared by every garbled request, so the
// fallback fails closed. Putting any proxy (a CDN) in front of Caddy makes the
// rightmost entry that proxy's address; change this first (docs/operations.md,
// "Reverse proxy trust and logging").
func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if lines := r.Header.Values("X-Forwarded-For"); len(lines) > 0 {
			last := lines[len(lines)-1]
			entry := strings.TrimSpace(last[strings.LastIndex(last, ",")+1:])
			if a, err := netip.ParseAddr(entry); err == nil {
				return a.WithZone("").String()
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
```

`strings.LastIndex` returns -1 when there is no comma, so `+1` takes the whole line. `net/netip` is already imported for `LimiterKey`.

- [ ] **Step 4: Run the web tests**

Run: `go test ./internal/web/ -count=1`
Expected: PASS. `TestLimiterKey` and `TestRateLimitIPv6Prefix` pass unchanged, since they don't set the header.

- [ ] **Step 5: Run the admin tests, which reach `ClientIP` through `a.caller`**

Run: `go test ./internal/admin/ -count=1`
Expected: PASS. The admin tests set `RemoteAddr` and not the header, so no expectation changes.

- [ ] **Step 6: Commit**

```bash
git add internal/web/middleware.go internal/web/middleware_test.go
git commit -m "web: take the client IP from the rightmost X-Forwarded-For entry (#10)"
```

### Task 2: Write the one-hop rule down

**Files:**
- Modify: `deploy/Caddyfile:17-20` (the `reverse_proxy` comment)
- Modify: `docs/operations.md:277` (first bullet of "Reverse proxy trust and logging")
- Modify: `docs/superpowers/specs/2026-09-24-thymeandplace-core-engine-design.md` §14 (add one bullet after the "Health" bullet)

**Interfaces:**
- Consumes: Task 1's behaviour.
- Produces: documentation only.

- [ ] **Step 1: Update the Caddyfile comment**

Replace the four comment lines starting `# reverse_proxy below depends on Caddy's default...` with:

```
	# The app trusts exactly one proxy hop, this Caddy, and takes the client IP
	# from the rightmost X-Forwarded-For entry (web.ClientIP). With no
	# trusted_proxies configured, Caddy discards any inbound X-Forwarded-For and
	# writes its own peer address, so that entry is the real client. Setting
	# trusted_proxies (for a CDN in front) makes Caddy keep the inbound values
	# and append the CDN edge, so every visitor would share one rate-limit
	# bucket. Change web.ClientIP before putting anything in front of Caddy.
```

- [ ] **Step 2: Update `docs/operations.md`**

Replace the first bullet under "## Reverse proxy trust and logging" (line 277) with:

```markdown
- The app trusts `X-Forwarded-*` headers (`TRUST_PROXY_HEADERS=true`, set in `deploy/compose.yaml`) from **exactly one proxy hop, Caddy**, and uses the **rightmost** `X-Forwarded-For` entry as the client IP (`web.ClientIP`). Caddy has no `trusted_proxies`, so it discards any `X-Forwarded-For` a client sends and writes the address it saw. If the rightmost entry isn't a valid IP, the app uses Caddy's own address, which is one shared bucket, so a garbled header can't earn a fresh rate-limit budget. **Before putting a CDN or any other proxy in front of Caddy, change `web.ClientIP` first.** With `trusted_proxies` set, the rightmost entry becomes the CDN edge, and every visitor behind it would share one public rate-limit bucket and one admin sign-in budget. The recommended change is to have Caddy send `header_up X-Client-IP {client_ip}` and have the app read only that header (spec `2026-09-27-admin-abuse-resistance-design.md` §8 question 3). **Never publish the app's own port** (`8080`) alongside Caddy's 80/443. If a client could reach the app directly, it could forge those headers and spoof its source IP past the admin sign-in budget. `deploy/compose.yaml` intentionally has no `ports:` entry under `app`; only `caddy` publishes ports.
```

- [ ] **Step 3: Amend core spec §14**

In `docs/superpowers/specs/2026-09-24-thymeandplace-core-engine-design.md`, after the line starting `- Health: Compose health checks`, insert:

```markdown
- Client IP: the app trusts exactly one proxy hop (Caddy) and uses the rightmost `X-Forwarded-For` entry. Putting any proxy in front of Caddy requires changing that first.
```

- [ ] **Step 4: Check the Caddyfile still parses**

Run: `docker run --rm -v "$PWD/deploy/Caddyfile:/etc/caddy/Caddyfile:ro" caddy:2 caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile`
Expected: `Valid configuration`. Comments can't break parsing, but this check is cheap.

- [ ] **Step 5: Commit**

```bash
git add deploy/Caddyfile docs/operations.md docs/superpowers/specs/2026-09-24-thymeandplace-core-engine-design.md
git commit -m "docs: record the one-hop proxy trust rule for the client IP (#10)"
```

### Task 3: Whole-repo check

- [ ] **Step 1: Run every Go test**

Run: `go vet ./... && go test ./... -count=1`
Expected: PASS.

- [ ] **Step 2: Run the e2e suite if Docker is available**

Run: `./e2e/run.sh`
Expected: PASS. Every e2e request arrives through Caddy from one address, so the rightmost entry equals the old leftmost one.
