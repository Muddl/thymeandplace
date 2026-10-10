package monitor

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const testUUID = "5b2c1a9e-0000-4000-8000-00000000abcd"

type recorded struct{ method, path, body, ctype string }

type receiver struct {
	mu   sync.Mutex
	got  []recorded
	code []int // status per request, then 200
}

func (rc *receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	rc.mu.Lock()
	rc.got = append(rc.got, recorded{r.Method, r.URL.Path, string(b), r.Header.Get("Content-Type")})
	status := http.StatusOK
	if len(rc.code) > 0 {
		status, rc.code = rc.code[0], rc.code[1:]
	}
	rc.mu.Unlock()
	w.WriteHeader(status)
}

func (rc *receiver) requests() []recorded {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return append([]recorded(nil), rc.got...)
}

// fast shrinks timeouts and backoff so failure tests stay quick.
func fast(p *Pinger) *Pinger {
	p.client.Timeout = 200 * time.Millisecond
	p.backoff = []time.Duration{10 * time.Millisecond, 20 * time.Millisecond}
	return p
}

// hangServer accepts a ping and never answers until the client goes away.
// It drains the body first: the server only notices a client disconnect
// once the body has reached EOF, so without it Close would block forever.
func hangServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
}

func TestPingerSuccessAndFail(t *testing.T) {
	rc := &receiver{}
	srv := httptest.NewServer(rc)
	defer srv.Close()
	p := NewPinger("app", srv.URL+"/"+testUUID, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.Success(context.Background(), "ok requests=3 5xx=0")
	p.Fail(context.Background(), strings.Repeat("x", 20_000))
	got := rc.requests()
	if len(got) != 2 {
		t.Fatalf("requests: %+v", got)
	}
	if got[0].method != "POST" || got[0].path != "/"+testUUID || got[0].body != "ok requests=3 5xx=0" || !strings.HasPrefix(got[0].ctype, "text/plain") {
		t.Fatalf("success ping: %+v", got[0])
	}
	if got[1].path != "/"+testUUID+"/fail" || len(got[1].body) != 10<<10 {
		t.Fatalf("fail ping: path %q, body %d bytes", got[1].path, len(got[1].body))
	}
}

func TestPingerDisabled(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	if p := NewPinger("app", "", log); p.Enabled() {
		t.Fatal("empty URL must disable the pinger")
	}
	if logs.Len() != 0 {
		t.Fatalf("an empty URL is not an error: %s", logs.String())
	}
	for _, bad := range []string{"http://hc-ping.com/" + testUUID, "::not a url", "ftp://127.0.0.1/x", "/relative/" + testUUID} {
		logs.Reset()
		p := NewPinger("drift", bad, log)
		if p.Enabled() {
			t.Fatalf("%q must be refused", bad)
		}
		if n := strings.Count(logs.String(), "heartbeat URL invalid; check disabled"); n != 1 || !strings.Contains(logs.String(), "level=ERROR") || strings.Contains(logs.String(), testUUID) {
			t.Fatalf("%q: want one ERROR without the URL, got:\n%s", bad, logs.String())
		}
		p.Success(context.Background(), "x") // must not panic or send
	}
	if !NewPinger("app", "https://hc-ping.com/"+testUUID, log).Enabled() || !NewPinger("app", "http://localhost:1/"+testUUID, log).Enabled() {
		t.Fatal("https and loopback http must be accepted")
	}
}

func TestPingerRetries(t *testing.T) {
	rc := &receiver{code: []int{500}}
	srv := httptest.NewServer(rc)
	defer srv.Close()
	p := fast(NewPinger("app", srv.URL+"/"+testUUID, slog.New(slog.NewTextHandler(io.Discard, nil))))
	p.Success(context.Background(), "ok")
	if n := len(rc.requests()); n != 2 {
		t.Fatalf("a 500 then a 200 must take 2 requests, got %d", n)
	}
}

func TestPingerNeverLogsURL(t *testing.T) {
	hang := hangServer()
	defer hang.Close()
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()
	for _, base := range []string{hang.URL, closedURL} {
		var logs bytes.Buffer
		p := fast(NewPinger("backup", base+"/"+testUUID, slog.New(slog.NewTextHandler(&logs, nil))))
		start := time.Now()
		p.Fail(context.Background(), "x")
		if took := time.Since(start); took > 2*time.Second {
			t.Fatalf("ping took %v; attempts must time out", took)
		}
		out := logs.String()
		if !strings.Contains(out, "heartbeat ping failed") || !strings.Contains(out, "level=WARN") || strings.Contains(out, testUUID) || strings.Contains(out, "level=ERROR") {
			t.Fatalf("want one WARN without the URL:\n%s", out)
		}
	}
}

// A ping in flight when the context is cancelled (shutdown) returns
// promptly, without waiting out the client timeout, and logs nothing.
func TestPingerCancelInFlight(t *testing.T) {
	hang := hangServer()
	defer hang.Close()
	var logs bytes.Buffer
	p := NewPinger("app", hang.URL+"/"+testUUID, slog.New(slog.NewTextHandler(&logs, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	p.Fail(ctx, "x")
	if took := time.Since(start); took > time.Second {
		t.Fatalf("cancelled ping took %v; it must return promptly", took)
	}
	if logs.Len() != 0 {
		t.Fatalf("a cancelled ping is not a failure: %s", logs.String())
	}
}
