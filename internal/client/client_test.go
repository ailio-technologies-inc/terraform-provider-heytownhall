package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const fakeToken = "htd_mgmt_FAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE000"

type recordingLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *recordingLogger) Debug(_ context.Context, msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprint(msg, fields))
}

func newTestClient(t *testing.T, h http.Handler, waits *[]time.Duration, lg Logger) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(Config{
		Endpoint: srv.URL, Token: fakeToken, Version: "9.9.9", Logger: lg,
		Sleep: func(_ context.Context, d time.Duration) error {
			if waits != nil {
				*waits = append(*waits, d)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCheckToken(t *testing.T) {
	for _, tc := range []struct{ token, want string }{
		{"", "no management token"},
		{"htd_test_abc", "data API key"},
		{"htd_live_abc", "data API key"},
		{"sk_whatever", "not a management token"},
		{fakeToken, ""},
	} {
		err := CheckToken(tc.token)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%q: unexpected %v", tc.token, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%q: got %v, want %q", tc.token, err, tc.want)
		}
		if err != nil && tc.token != "" && strings.Contains(err.Error(), tc.token) {
			t.Errorf("the error repeats the token: %v", err)
		}
	}
}

func TestHeadersAndUserAgent(t *testing.T) {
	var got http.Header
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = w.Write([]byte(`{"id":"o1","name":"Acme"}`))
	}), nil, nil)
	o, err := c.GetOrganization(context.Background())
	if err != nil || o.Name != "Acme" {
		t.Fatalf("%v %v", o, err)
	}
	if got.Get("User-Agent") != "terraform-provider-heytownhall/9.9.9" {
		t.Errorf("user agent %q", got.Get("User-Agent"))
	}
	if got.Get("Authorization") != "Bearer "+fakeToken {
		t.Errorf("authorization header missing")
	}
	if got.Get("Idempotency-Key") != "" {
		t.Errorf("a GET must not carry an idempotency key")
	}
}

func TestCoverageSendsNoToken(t *testing.T) {
	var auth string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"results":[{"id":"victoria","kind":"city","name":"Victoria"}],"next_cursor":null}`))
	}), nil, nil)
	rows, err := c.ListCoverage(context.Background(), "", "", "")
	if err != nil || len(rows) != 1 || auth != "" {
		t.Fatalf("rows=%v err=%v auth=%q", rows, err, auth)
	}
}

func TestRetryOn429HonoursRetryAfter(t *testing.T) {
	var n int
	var waits []time.Duration
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if n < 3 {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"error":{"code":"rate_limited","message":"slow down","request_id":"r1"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"o1","name":"Acme"}`))
	}), &waits, nil)
	if _, err := c.GetOrganization(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n != 3 || len(waits) != 2 || waits[0] != 7*time.Second || waits[1] != 7*time.Second {
		t.Fatalf("attempts=%d waits=%v", n, waits)
	}
}

func TestRetryBacksOffOn5xxThenGivesUp(t *testing.T) {
	for _, status := range []int{502, 503, 504} {
		var n int
		var waits []time.Duration
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n++
			w.WriteHeader(status)
		}), &waits, nil)
		_, err := c.GetOrganization(context.Background())
		var ae *APIError
		if !errors.As(err, &ae) || ae.Status != status {
			t.Fatalf("status %d: got %v", status, err)
		}
		if n != 6 || len(waits) != 5 {
			t.Fatalf("status %d: attempts=%d waits=%v", status, n, waits)
		}
		for i := 1; i < len(waits); i++ {
			if waits[i] <= waits[i-1] {
				t.Errorf("backoff does not grow: %v", waits)
			}
		}
	}
}

func TestNoRetryOn4xx(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 409, 422, 500} {
		var n int
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n++
			w.WriteHeader(status)
		}), nil, nil)
		if _, err := c.GetOrganization(context.Background()); err == nil || n != 1 {
			t.Fatalf("status %d: attempts=%d err=%v", status, n, err)
		}
	}
}

func TestIdempotencyKeyIsGeneratedOncePerCreateAndReused(t *testing.T) {
	var keys []string
	var n int
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		n++
		if n < 3 {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"key":{"id":"k1","name":"a","kind":"sandbox"},"secret":"htd_test_SECRETSECRET"}`))
	}), nil, nil)
	_, secret, err := c.CreateAPIKey(context.Background(), "a", "sandbox")
	if err != nil || secret == nil || *secret != "htd_test_SECRETSECRET" {
		t.Fatalf("%v", err)
	}
	if len(keys) != 3 || keys[0] == "" || keys[0] != keys[1] || keys[1] != keys[2] {
		t.Fatalf("keys %v", keys)
	}
	// A second create is a new operation and gets its own key.
	n = 2
	if _, _, err := c.CreateAPIKey(context.Background(), "b", "sandbox"); err != nil {
		t.Fatal(err)
	}
	if keys[len(keys)-1] == keys[0] {
		t.Fatalf("a second create reused the first key")
	}
}

func TestReplayGivesNilSecret(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Idempotent-Replayed", "true")
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"key":{"id":"k1","name":"a","kind":"sandbox"},"secret":null}`))
	}), nil, nil)
	k, secret, err := c.CreateAPIKey(context.Background(), "a", "sandbox")
	if err != nil || k.ID != "k1" || secret != nil {
		t.Fatalf("%v %v %v", k, secret, err)
	}
}

func TestPostWithoutKeyIsNotRetriedOnTransportError(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		hj, _ := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		_ = conn.Close()
	}))
	defer srv.Close()
	c, _ := New(Config{Endpoint: srv.URL, Token: fakeToken, Sleep: func(context.Context, time.Duration) error { return nil }})
	// verify is a POST without a key.
	if _, err := c.VerifySSODomain(context.Background(), "c", "d"); err == nil {
		t.Fatal("expected an error")
	}
	if n.Load() != 1 {
		t.Fatalf("a POST without an idempotency key was retried %d times", n.Load())
	}
}

func TestErrorEnvelopeBecomesAPIError(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"error":{"code":"owner_protected","message":"Owners are managed in the dashboard.","param":"role","request_id":"req_42"}}`))
	}), nil, nil)
	_, err := c.UpdateMember(context.Background(), "u1", "member")
	ae, ok := AsAPIError(err)
	if !ok || ae.Status != 403 || ae.Code != "owner_protected" || ae.Param != "role" || ae.RequestID != "req_42" {
		t.Fatalf("%#v", err)
	}
	for _, want := range []string{"403", "owner_protected", "Owners are managed in the dashboard.", "role", "req_42"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error text lacks %q: %s", want, err)
		}
	}
	if !HasCode(err, "owner_protected") || IsNotFound(err) {
		t.Errorf("helpers wrong")
	}
}

func TestNonEnvelopeErrorIsKeptShort(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte(strings.Repeat("x", 1000)))
	}), nil, nil)
	_, err := c.GetOrganization(context.Background())
	if !IsNotFound(err) || len(err.Error()) > 300 {
		t.Fatalf("%v", err)
	}
}

func TestPaginationFollowsCursor(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("cursor") {
		case "":
			_, _ = w.Write([]byte(`{"data":[{"id":"a"},{"id":"b"}],"next_cursor":"c1"}`))
		case "c1":
			_, _ = w.Write([]byte(`{"data":[{"id":"c"}],"next_cursor":null}`))
		}
	}), nil, nil)
	keys, err := c.ListAPIKeys(context.Background())
	if err != nil || len(keys) != 3 || keys[2].ID != "c" {
		t.Fatalf("%v %v", keys, err)
	}
}

func TestPaginationStopsOnRepeatedCursor(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"a"}],"next_cursor":"same"}`))
	}), nil, nil)
	if _, err := c.ListAPIKeys(context.Background()); err == nil {
		t.Fatal("expected a loop error")
	}
}

func TestNoSecretInLogsOrErrors(t *testing.T) {
	const secret = "htd_test_SUPERSECRETVALUE"
	lg := &recordingLogger{}
	var n int
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if n == 1 {
			w.WriteHeader(503)
			return
		}
		if n == 2 {
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"key":{"id":"k1","name":"a","kind":"sandbox"},"secret":"` + secret + `"}`))
			return
		}
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"bad token","request_id":"r9"}}`))
	}), nil, lg)
	if _, _, err := c.CreateAPIKey(context.Background(), "a", "sandbox"); err != nil {
		t.Fatal(err)
	}
	_, err := c.GetOrganization(context.Background())
	all := strings.Join(lg.lines, "\n") + "\n" + err.Error()
	for _, bad := range []string{fakeToken, secret, "Bearer", "Authorization", "Idempotency-Key"} {
		if strings.Contains(all, bad) {
			t.Errorf("logs or errors contain %q:\n%s", bad, all)
		}
	}
	if len(lg.lines) != 3 {
		t.Errorf("want one log line per attempt, got %d", len(lg.lines))
	}
}

func TestTransportErrorDoesNotLeakURLOrToken(t *testing.T) {
	c, _ := New(Config{Endpoint: "http://127.0.0.1:1", Token: fakeToken, MaxRetries: 1, Sleep: func(context.Context, time.Duration) error { return nil }})
	_, err := c.GetOrganization(context.Background())
	if err == nil || strings.Contains(err.Error(), fakeToken) {
		t.Fatalf("%v", err)
	}
}

func TestRetryAfterDateAndCap(t *testing.T) {
	if _, ok := retryAfter(""); ok {
		t.Fatal("empty")
	}
	if _, ok := retryAfter("nonsense"); ok {
		t.Fatal("nonsense")
	}
	if d, ok := retryAfter("3"); !ok || d != 3*time.Second {
		t.Fatal("seconds")
	}
	if d, ok := retryAfter("0"); !ok || d != 0 {
		t.Fatal("zero seconds means retry now")
	}
	d, _ := retryAfter(time.Now().Add(10 * time.Second).UTC().Format(http.TimeFormat))
	if d < 8*time.Second || d > 10*time.Second {
		t.Fatalf("date form: %v", d)
	}
}

func TestContextCancelStopsRetrying(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		w.WriteHeader(503)
	}))
	defer srv.Close()
	c, _ := New(Config{Endpoint: srv.URL, Token: fakeToken, Sleep: func(ctx context.Context, d time.Duration) error { cancel(); return ctx.Err() }})
	if _, err := c.GetOrganization(ctx); err == nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestNewRejectsBadEndpoint(t *testing.T) {
	if _, err := New(Config{Endpoint: "not a url", Token: fakeToken}); err == nil {
		t.Fatal("expected error")
	}
}

func TestBudgetErrorsOn429AreNotRetried(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(c *Client) error
		body string
	}{
		{"another code", func(c *Client) error { _, err := c.GetOrganization(context.Background()); return err }, `{"error":{"code":"invitation_budget_spent","message":"x","request_id":"r"}}`},
		{"invitation create", func(c *Client) error {
			_, err := c.CreateInvitation(context.Background(), "a@b.test", "member")
			return err
		}, `{"error":{"code":"rate_limited","message":"An organisation can send 50 invitations in 24 hours.","request_id":"r"}}`},
	} {
		var n atomic.Int32
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n.Add(1)
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(429)
			_, _ = w.Write([]byte(tc.body))
		}), nil, nil)
		err := tc.call(c)
		if ae, ok := AsAPIError(err); !ok || ae.Status != 429 || n.Load() != 1 {
			t.Errorf("%s: attempts=%d err=%v", tc.name, n.Load(), err)
		}
	}
}
