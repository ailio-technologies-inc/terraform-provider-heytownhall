// Package client is a small typed client for the Hey Town Hall Data management API.
package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultEndpoint is the production API.
const DefaultEndpoint = "https://data.heytownhall.com"

// ManagementTokenPrefix starts every management token.
const ManagementTokenPrefix = "htd_mgmt_"

const (
	maxResponseBytes = 8 << 20
	maxRetryWait     = 2 * time.Minute
)

// Logger receives one line per request attempt. The client passes only the method, the path,
// the status, the attempt number and the request id. It never passes headers or bodies.
type Logger interface {
	Debug(ctx context.Context, msg string, fields map[string]any)
}

// Config configures a Client.
type Config struct {
	Endpoint   string
	Token      string
	Version    string
	HTTPClient *http.Client
	// MaxRetries is how many times a request is retried after the first attempt. Default 5.
	MaxRetries int
	Logger     Logger
	// Sleep waits between attempts. Tests replace it.
	Sleep func(ctx context.Context, d time.Duration) error
}

// Client calls the management API.
type Client struct {
	endpoint   *url.URL
	token      string
	userAgent  string
	http       *http.Client
	maxRetries int
	logger     Logger
	sleep      func(ctx context.Context, d time.Duration) error
}

// New checks the configuration and returns a Client.
func New(cfg Config) (*Client, error) {
	ep := cfg.Endpoint
	if ep == "" {
		ep = DefaultEndpoint
	}
	u, err := url.Parse(strings.TrimRight(ep, "/"))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("endpoint %q is not a URL with a scheme and a host", ep)
	}
	if err := CheckToken(cfg.Token); err != nil {
		return nil, err
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	}
	// A copy that never follows a redirect: the API does not redirect, and a redirect is not worth carrying the token to.
	hcCopy := *hc
	hcCopy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	retries := cfg.MaxRetries
	if retries == 0 {
		retries = 5
	}
	sleep := cfg.Sleep
	if sleep == nil {
		sleep = sleepContext
	}
	version := cfg.Version
	if version == "" {
		version = "dev"
	}
	return &Client{
		endpoint:   u,
		token:      cfg.Token,
		userAgent:  "terraform-provider-heytownhall/" + version,
		http:       &hcCopy,
		maxRetries: retries,
		logger:     cfg.Logger,
		sleep:      sleep,
	}, nil
}

// CheckToken explains what is wrong with a token, without repeating it.
func CheckToken(token string) error {
	switch {
	case token == "":
		return errors.New("no management token is set. Create one in the dashboard (Settings, Management tokens) and set the provider's token argument or the HEYTOWNHALL_MANAGEMENT_TOKEN environment variable")
	case strings.HasPrefix(token, "htd_test_") || strings.HasPrefix(token, "htd_live_"):
		return errors.New("the token is a data API key (htd_test_ or htd_live_), which cannot manage an organisation. Use a management token, which starts with htd_mgmt_")
	case !strings.HasPrefix(token, ManagementTokenPrefix):
		return errors.New("the token is not a management token. A management token starts with htd_mgmt_")
	}
	return nil
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type reqOpts struct {
	query      url.Values
	body       any
	idempotent bool // POST create: send one Idempotency-Key for the call and all its retries
	noAuth     bool // public route: do not send the token
}

// NewIdempotencyKey returns a random key.
func NewIdempotencyKey() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func (c *Client) do(ctx context.Context, method, path string, o reqOpts) (json.RawMessage, error) {
	u := *c.endpoint
	u.Path = strings.TrimRight(u.Path, "/") + path
	if o.query != nil {
		u.RawQuery = o.query.Encode()
	}
	var payload []byte
	if o.body != nil {
		var err error
		if payload, err = json.Marshal(o.body); err != nil {
			return nil, fmt.Errorf("encode request: %w", err)
		}
	}
	idemKey := ""
	if o.idempotent {
		idemKey = NewIdempotencyKey() // once per create, reused on every retry
	}
	retrySafe := method != http.MethodPost || idemKey != ""

	var lastErr error
	for attempt := 0; ; attempt++ {
		var body io.Reader
		if payload != nil {
			body = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", c.userAgent)
		req.Header.Set("Accept", "application/json")
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if !o.noAuth {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		if idemKey != "" {
			req.Header.Set("Idempotency-Key", idemKey)
		}

		resp, err := c.http.Do(req)
		var raw []byte
		status := 0
		var wait time.Duration
		hasWait := false
		if err == nil {
			status = resp.StatusCode
			raw, err = io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
			_ = resp.Body.Close()
			wait, hasWait = retryAfter(resp.Header.Get("Retry-After"))
		}
		if c.logger != nil {
			f := map[string]any{"method": method, "path": path, "attempt": attempt + 1}
			if status != 0 {
				f["status"] = status
				f["request_id"] = resp.Header.Get("X-Request-Id")
			}
			c.logger.Debug(ctx, "heytownhall API request", f)
		}

		retryable := false
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = fmt.Errorf("%s %s: %w", method, path, stripURL(err))
			retryable = retrySafe
		case status == 429 || status == 502 || status == 503 || status == 504:
			ae := parseAPIError(status, raw)
			lastErr = ae
			retryable = true
			if status == 429 && !isRateLimit(method, path, ae) {
				// A 429 that is a budget (for example invitations per day), not the per-minute limit: waiting a minute will not help.
				return nil, ae
			}
		case status >= 400:
			return nil, parseAPIError(status, raw)
		default:
			return raw, nil
		}
		if !retryable || attempt >= c.maxRetries {
			return nil, lastErr
		}
		if !hasWait {
			wait = backoff(attempt)
		}
		if wait > maxRetryWait {
			wait = maxRetryWait
		}
		if err := c.sleep(ctx, wait); err != nil {
			return nil, err
		}
	}
}

// stripURL drops the URL that net/http puts in its errors, keeping the cause.
func stripURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func backoff(attempt int) time.Duration {
	d := 500 * time.Millisecond << attempt
	if d > 30*time.Second || d <= 0 {
		d = 30 * time.Second
	}
	return d
}

// retryAfter reads a Retry-After header in seconds or as an HTTP date. The bool is false when there is none to honour.
func retryAfter(h string) (time.Duration, bool) {
	h = strings.TrimSpace(h)
	if h == "" {
		return 0, false
	}
	if n, err := strconv.Atoi(h); err == nil && n >= 0 {
		return time.Duration(n) * time.Second, true
	}
	if t, err := http.ParseTime(h); err == nil {
		if d := time.Until(t); d > 0 {
			return d, true
		}
		return 0, true
	}
	return 0, false
}

// call sends a request and decodes the answer into out (which may be nil).
// wrapper names a key the answer may be nested under; the object is read from it when it is there.
func (c *Client) call(ctx context.Context, method, path string, o reqOpts, wrapper string, out any) error {
	raw, err := c.do(ctx, method, path, o)
	if err != nil {
		return err
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	return decodeObject(raw, wrapper, out)
}

func decodeObject(raw json.RawMessage, wrapper string, out any) error {
	if wrapper != "" {
		var m map[string]json.RawMessage
		if json.Unmarshal(raw, &m) == nil {
			if inner, ok := m[wrapper]; ok && len(inner) > 0 && inner[0] == '{' {
				raw = inner
			}
		}
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

type page struct {
	Data       []json.RawMessage `json:"data"`
	Results    []json.RawMessage `json:"results"`
	NextCursor *string           `json:"next_cursor"`
}

// listAll follows next_cursor until the list ends. Management lists answer `data`; the public data API answers `results`.
func listAll[T any](ctx context.Context, c *Client, path string, q url.Values, noAuth bool) ([]T, error) {
	var all []T
	cursor := ""
	seen := map[string]bool{}
	for {
		qq := url.Values{}
		for k, v := range q {
			qq[k] = v
		}
		qq.Set("limit", "100")
		if cursor != "" {
			qq.Set("cursor", cursor)
		}
		raw, err := c.do(ctx, http.MethodGet, path, reqOpts{query: qq, noAuth: noAuth})
		if err != nil {
			return nil, err
		}
		var p page
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("decode list %s: %w", path, err)
		}
		items := p.Data
		if items == nil {
			items = p.Results
		}
		for _, it := range items {
			var v T
			if err := json.Unmarshal(it, &v); err != nil {
				return nil, fmt.Errorf("decode list %s: %w", path, err)
			}
			all = append(all, v)
		}
		if p.NextCursor == nil || *p.NextCursor == "" {
			return all, nil
		}
		if seen[*p.NextCursor] {
			return nil, fmt.Errorf("list %s: the API returned the cursor %q twice", path, *p.NextCursor)
		}
		seen[*p.NextCursor] = true
		cursor = *p.NextCursor
	}
}

// isRateLimit reports whether a 429 is the per-minute request limit, which a short wait clears. Any other 429 code, and any
// 429 on an invitation create (which also has a daily budget), is final.
func isRateLimit(method, path string, e *APIError) bool {
	if method == http.MethodPost && strings.HasSuffix(path, "/invitations") {
		return false
	}
	return e.Code == "" || e.Code == "rate_limited"
}
