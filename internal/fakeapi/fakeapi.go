// Package fakeapi is an in-process fake of the Hey Town Hall Data management API, for tests.
// It follows the accepted design (section 5 of the SSO and management design note): owner protection,
// revoked keys that still read back, idempotent replays with a null secret, cursor pagination,
// 429 with Retry-After, and domain proof by a DNS TXT record that a test publishes into the fake.
package fakeapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Token is the one management token the fake accepts.
const Token = "htd_mgmt_FAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE000"

// Request is one call the fake saw.
type Request struct {
	Method, Path, IdempotencyKey, UserAgent string
	HadAuthorization                        bool
}

type key struct {
	ID, Name, Kind, Display string
	CreatedAt               time.Time
	RevokedAt               *time.Time
}

type member struct {
	UserID, Name, Email, Role string
	JoinedAt                  time.Time
}

type invitation struct {
	ID, Email, Role, Status string
	AcceptedUserID          string
	ExpiresAt, CreatedAt    time.Time
}

type domain struct {
	ID, ConnectionID, Domain, Token string
	VerifiedAt, CheckedAt           *time.Time
	CreatedAt                       time.Time
}

type connection struct {
	ID, Name, Issuer, ClientID, EmailClaim string
	Secret                                 string
	Trust, Enabled, Enforced               bool
	Domains                                []*domain
	CreatedAt                              time.Time
}

type idemEntry struct {
	bodyHash string
	status   int
	body     map[string]any
}

// Fake is the fake API.
type Fake struct {
	Server *httptest.Server

	mu         sync.Mutex
	pageSize   int
	seq        int
	orgID      string
	orgName    string
	capCents   *int64
	keys       []*key
	members    []*member
	invites    []*invitation
	conns      []*connection
	dns        map[string]string
	idem       map[string]idemEntry
	requests   []Request
	rate429    int
	dropCreate bool
	liveOpen   bool

	// propagateAfter simulates DNS propagation: the record counts as found once a domain has been checked more than this many times. -1 = never.
	propagateAfter int
	checks         map[string]int
}

// New starts a fake with one organisation, one owner and a page size of 2.
func New() *Fake {
	f := &Fake{pageSize: 2, orgID: "00000000-0000-4000-8000-0000000000a0", orgName: "Acme Council Data", dns: map[string]string{}, idem: map[string]idemEntry{}, liveOpen: true, propagateAfter: -1, checks: map[string]int{}}
	f.members = []*member{{UserID: f.id(), Name: "Olive Owner", Email: "owner@acme.test", Role: "owner", JoinedAt: time.Now().UTC()}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

// Close stops the server.
func (f *Fake) Close() { f.Server.Close() }

// URL is the endpoint to give the provider.
func (f *Fake) URL() string { return f.Server.URL }

func (f *Fake) id() string {
	f.seq++
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", f.seq)
}

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339) }
func tsp(t *time.Time) any {
	if t == nil {
		return nil
	}
	return ts(*t)
}

// ---- controls for tests ----

// SetPageSize sets the most items a list answers per page.
func (f *Fake) SetPageSize(n int) { f.mu.Lock(); f.pageSize = n; f.mu.Unlock() }

// Inject429 makes the next n requests answer 429 with Retry-After: 0.
func (f *Fake) Inject429(n int) { f.mu.Lock(); f.rate429 = n; f.mu.Unlock() }

// DropNextCreateResponse makes the next create succeed, then answer 503 so the client retries into a replay.
func (f *Fake) DropNextCreateResponse() { f.mu.Lock(); f.dropCreate = true; f.mu.Unlock() }

// SetLiveOpen says whether live keys may be created.
func (f *Fake) SetLiveOpen(v bool) { f.mu.Lock(); f.liveOpen = v; f.mu.Unlock() }

// PropagateAfter makes the verifier find any domain's record once the domain has been checked more than n times.
func (f *Fake) PropagateAfter(n int) { f.mu.Lock(); f.propagateAfter = n; f.mu.Unlock() }

// PublishTXT puts a TXT record where the fake's verifier will find it.
func (f *Fake) PublishTXT(name, value string) {
	f.mu.Lock()
	f.dns[strings.ToLower(name)] = value
	f.mu.Unlock()
}

// RevokeKey revokes a key as if done in the dashboard.
func (f *Fake) RevokeKey(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, k := range f.keys {
		if k.ID == id {
			n := time.Now()
			k.RevokedAt = &n
		}
	}
}

// AcceptInvitation accepts the pending invitation for email, makes the member and returns the user id.
func (f *Fake) AcceptInvitation(email string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, inv := range f.invites {
		if inv.Status == "pending" && strings.EqualFold(inv.Email, email) {
			m := &member{UserID: f.id(), Name: strings.Split(inv.Email, "@")[0], Email: inv.Email, Role: inv.Role, JoinedAt: time.Now().UTC()}
			f.members = append(f.members, m)
			inv.Status, inv.AcceptedUserID = "accepted", m.UserID
			return m.UserID
		}
	}
	panic("no pending invitation for " + email)
}

// ExpireInvitation makes the pending invitation for email expired.
func (f *Fake) ExpireInvitation(email string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, inv := range f.invites {
		if inv.Status == "pending" && strings.EqualFold(inv.Email, email) {
			inv.Status = "expired"
		}
	}
}

// AddMember adds a member directly.
func (f *Fake) AddMember(email, role string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := &member{UserID: f.id(), Name: strings.Split(email, "@")[0], Email: email, Role: role, JoinedAt: time.Now().UTC()}
	f.members = append(f.members, m)
	return m.UserID
}

// RemoveMember removes a member as if done in the dashboard.
func (f *Fake) RemoveMember(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i, m := f.findMember(id); m != nil {
		f.members = append(f.members[:i], f.members[i+1:]...)
	}
}

// OwnerID is the seeded owner's user id.
func (f *Fake) OwnerID() string { f.mu.Lock(); defer f.mu.Unlock(); return f.members[0].UserID }

// MemberRole reads a member's role; ok is false when there is none.
func (f *Fake) MemberRole(userID string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.members {
		if m.UserID == userID {
			return m.Role, true
		}
	}
	return "", false
}

// KeyState reports a key's name, kind and whether it is revoked.
func (f *Fake) KeyState(id string) (name, kind string, revoked, found bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, k := range f.keys {
		if k.ID == id {
			return k.Name, k.Kind, k.RevokedAt != nil, true
		}
	}
	return
}

// KeyCount counts keys that are not revoked.
func (f *Fake) KeyCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, k := range f.keys {
		if k.RevokedAt == nil {
			n++
		}
	}
	return n
}

// InvitationCount counts pending invitations.
func (f *Fake) InvitationCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, i := range f.invites {
		if i.Status == "pending" {
			n++
		}
	}
	return n
}

// SpendCap reads the cap the fake holds.
func (f *Fake) SpendCap() *int64 { f.mu.Lock(); defer f.mu.Unlock(); return f.capCents }

// SetSpendCap changes the cap as if done in the dashboard.
func (f *Fake) SetSpendCap(c *int64) { f.mu.Lock(); f.capCents = c; f.mu.Unlock() }

// OrgName reads the name the fake holds.
func (f *Fake) OrgName() string { f.mu.Lock(); defer f.mu.Unlock(); return f.orgName }

// SetOrgName renames as if done in the dashboard.
func (f *Fake) SetOrgName(n string) { f.mu.Lock(); f.orgName = n; f.mu.Unlock() }

// ConnectionSecret returns the secret last written for a connection (the API never returns it).
func (f *Fake) ConnectionSecret(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.conns {
		if c.ID == id {
			return c.Secret
		}
	}
	return ""
}

// ConnectionState reports enabled and enforced.
func (f *Fake) ConnectionState(id string) (enabled, enforced, found bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.conns {
		if c.ID == id {
			return c.Enabled, c.Enforced, true
		}
	}
	return
}

// SetClaimSettings changes the email claim and trust setting as an owner would in the dashboard, for every connection.
func (f *Fake) SetClaimSettings(claim string, trust bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.conns {
		c.EmailClaim, c.Trust = claim, trust
	}
}

// ConnectionCount counts connections.
func (f *Fake) ConnectionCount() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.conns) }

// DomainCount counts domains on all connections.
func (f *Fake) DomainCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.conns {
		n += len(c.Domains)
	}
	return n
}

// DomainVerified reports whether a domain is verified.
func (f *Fake) DomainVerified(domainName string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.conns {
		for _, d := range c.Domains {
			if d.Domain == domainName && d.VerifiedAt != nil {
				return true
			}
		}
	}
	return false
}

// Requests returns every call seen so far.
func (f *Fake) Requests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Request(nil), f.requests...)
}

// ---- HTTP ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func apiErr(w http.ResponseWriter, status int, code, msg, param string) {
	e := map[string]any{"code": code, "message": msg, "request_id": "req_fake"}
	if param != "" {
		e["param"] = param
	}
	writeJSON(w, status, map[string]any{"error": e})
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, Request{Method: r.Method, Path: r.URL.Path, IdempotencyKey: r.Header.Get("Idempotency-Key"), UserAgent: r.Header.Get("User-Agent"), HadAuthorization: r.Header.Get("Authorization") != ""})
	w.Header().Set("X-Request-Id", "req_fake")
	if f.rate429 > 0 {
		f.rate429--
		w.Header().Set("Retry-After", "0")
		apiErr(w, 429, "rate_limited", "Too many requests. Retry after the time in Retry-After.", "")
		return
	}
	if r.URL.Path == "/v1/coverage" {
		if r.Header.Get("Authorization") != "" {
			apiErr(w, 401, "unauthorized", "This route takes no credentials.", "")
			return
		}
		f.coverage(w, r)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/v1/management/") {
		apiErr(w, 404, "not_found", "No such route.", "")
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+Token {
		apiErr(w, 401, "unauthorized", "The management token is missing, wrong, revoked or expired.", "")
		return
	}
	p := strings.TrimPrefix(r.URL.Path, "/v1/management/")
	parts := strings.Split(p, "/")
	switch parts[0] {
	case "organization":
		f.organization(w, r)
	case "spend-cap":
		f.spendCap(w, r)
	case "api-keys":
		f.apiKeys(w, r, parts[1:])
	case "members":
		f.members_(w, r, parts[1:])
	case "invitations":
		f.invitations(w, r, parts[1:])
	case "sso-connections":
		f.sso(w, r, parts[1:])
	case "management-tokens":
		f.tokens(w, r, parts[1:])
	case "token":
		f.tokens(w, r, nil)
	case "usage":
		f.usage(w, r)
	default:
		apiErr(w, 404, "not_found", "No such route.", "")
	}
}

func readBody(r *http.Request) (map[string]any, string, bool) {
	raw, _ := io.ReadAll(r.Body)
	var m map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, "", false
		}
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, string(raw), true
}

func str(m map[string]any, k string) (string, bool) {
	v, ok := m[k]
	if !ok || v == nil {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// paginate writes one page of items (already ordered) using an offset cursor.
func (f *Fake) paginate(w http.ResponseWriter, r *http.Request, n int, field string, item func(i int) any) {
	limit := f.pageSize
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l >= 1 && l < limit {
		limit = l
	}
	start := 0
	if c := r.URL.Query().Get("cursor"); c != "" {
		v, err := strconv.Atoi(strings.TrimPrefix(c, "c"))
		if err != nil || v < 0 {
			apiErr(w, 400, "invalid_request", "cursor is not valid.", "cursor")
			return
		}
		start = v
	}
	end := start + limit
	var next any
	if end < n {
		next = "c" + strconv.Itoa(end)
	} else {
		end = n
	}
	items := []any{}
	for i := start; i < end && i < n; i++ {
		items = append(items, item(i))
	}
	writeJSON(w, 200, map[string]any{field: items, "next_cursor": next})
}

// idempotent wraps a create. fn returns the status and body; a replay answers the stored body with a null secret.
func (f *Fake) idempotent(w http.ResponseWriter, r *http.Request, raw string, fn func() (int, map[string]any)) {
	k := r.Header.Get("Idempotency-Key")
	sum := sha256.Sum256([]byte(raw))
	h := hex.EncodeToString(sum[:])
	if k != "" {
		if e, ok := f.idem[r.URL.Path+"|"+k]; ok {
			if e.bodyHash != h {
				apiErr(w, 409, "idempotency_conflict", "This Idempotency-Key was used with a different request.", "")
				return
			}
			w.Header().Set("Idempotent-Replayed", "true")
			writeJSON(w, e.status, e.body)
			return
		}
	}
	status, body := fn()
	if status >= 400 {
		writeJSON(w, status, body)
		return
	}
	if k != "" {
		stored := map[string]any{}
		for kk, vv := range body {
			stored[kk] = vv
		}
		if _, ok := stored["secret"]; ok {
			stored["secret"] = nil
		}
		f.idem[r.URL.Path+"|"+k] = idemEntry{bodyHash: h, status: status, body: stored}
	}
	if f.dropCreate {
		f.dropCreate = false
		w.WriteHeader(503)
		return
	}
	writeJSON(w, status, body)
}

func (f *Fake) organization(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
	case "PATCH":
		m, _, ok := readBody(r)
		name, has := str(m, "name")
		if !ok || !has || strings.TrimSpace(name) == "" {
			apiErr(w, 400, "invalid_request", "name must not be empty.", "name")
			return
		}
		f.orgName = name
	default:
		apiErr(w, 405, "method_not_allowed", "Method not allowed.", "")
		return
	}
	writeJSON(w, 200, map[string]any{"organization": map[string]any{"id": f.orgID, "name": f.orgName, "spend_cap_cents": f.capCents, "created_at": "2026-01-01T00:00:00Z"}})
}

func (f *Fake) spendCap(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
	case "PUT":
		m, _, ok := readBody(r)
		v, present := m["spend_cap_cents"]
		if !ok || !present {
			apiErr(w, 400, "invalid_request", "spend_cap_cents is required (a number or null).", "spend_cap_cents")
			return
		}
		if v == nil {
			f.capCents = nil
		} else if n, isNum := v.(float64); isNum && n >= 1 && n <= 1e10 && n == float64(int64(n)) {
			c := int64(n)
			f.capCents = &c
		} else {
			apiErr(w, 400, "invalid_request", "spend_cap_cents must be at least 1, or null for no cap.", "spend_cap_cents")
			return
		}
	default:
		apiErr(w, 405, "method_not_allowed", "Method not allowed.", "")
		return
	}
	writeJSON(w, 200, map[string]any{"spend_cap": map[string]any{"organization_id": f.orgID, "spend_cap_cents": f.capCents}})
}

func keyJSON(k *key) map[string]any {
	return map[string]any{"id": k.ID, "name": k.Name, "kind": k.Kind, "display": k.Display, "environment": k.Kind, "created_at": ts(k.CreatedAt), "last_used_at": nil, "revoked_at": tsp(k.RevokedAt)}
}

func (f *Fake) findKey(id string) *key {
	for _, k := range f.keys {
		if k.ID == id {
			return k
		}
	}
	return nil
}

func (f *Fake) apiKeys(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case "GET":
			f.paginate(w, r, len(f.keys), "data", func(i int) any { return keyJSON(f.keys[i]) })
		case "POST":
			m, raw, ok := readBody(r)
			f.idempotent(w, r, raw, func() (int, map[string]any) {
				name, _ := str(m, "name")
				kind, _ := str(m, "kind")
				if !ok || strings.TrimSpace(name) == "" {
					return 400, errBody("invalid_request", "name must not be empty.", "name")
				}
				if kind != "sandbox" && kind != "live" {
					return 400, errBody("invalid_request", "kind must be sandbox or live.", "kind")
				}
				if kind == "live" && !f.liveOpen {
					return 402, errBody("billing_required", "Live keys need a payment method.", "")
				}
				id := f.id()
				secret := "htd_" + map[string]string{"sandbox": "test", "live": "live"}[kind] + "_FAKESECRET" + id[len(id)-12:]
				k := &key{ID: id, Name: name, Kind: kind, Display: "htd_" + kind + "_…" + secret[len(secret)-4:], CreatedAt: time.Now().UTC()}
				f.keys = append(f.keys, k)
				return 201, map[string]any{"key": keyJSON(k), "secret": secret}
			})
		default:
			apiErr(w, 405, "method_not_allowed", "Method not allowed.", "")
		}
		return
	}
	k := f.findKey(rest[0])
	if k == nil {
		apiErr(w, 404, "not_found", "No such API key.", "")
		return
	}
	switch r.Method {
	case "GET":
		writeJSON(w, 200, map[string]any{"key": keyJSON(k)})
	case "PATCH":
		m, _, _ := readBody(r)
		name, ok := str(m, "name")
		if !ok || strings.TrimSpace(name) == "" {
			apiErr(w, 400, "invalid_request", "name must not be empty.", "name")
			return
		}
		k.Name = name
		writeJSON(w, 200, map[string]any{"key": keyJSON(k)})
	case "DELETE":
		if k.RevokedAt == nil {
			n := time.Now()
			k.RevokedAt = &n
		}
		writeJSON(w, 200, map[string]any{"key": keyJSON(k)})
	default:
		apiErr(w, 405, "method_not_allowed", "Method not allowed.", "")
	}
}

func errBody(code, msg, param string) map[string]any {
	e := map[string]any{"code": code, "message": msg, "request_id": "req_fake"}
	if param != "" {
		e["param"] = param
	}
	return map[string]any{"error": e}
}

func memberJSON(m *member) map[string]any {
	return map[string]any{"user_id": m.UserID, "name": m.Name, "email": m.Email, "role": m.Role, "joined_at": ts(m.JoinedAt)}
}

func (f *Fake) findMember(id string) (int, *member) {
	for i, m := range f.members {
		if m.UserID == id {
			return i, m
		}
	}
	return -1, nil
}

const ownerMsg = "A token cannot create, promote, demote or remove an owner. Manage owners in the dashboard."

func (f *Fake) members_(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		if r.Method != "GET" {
			apiErr(w, 405, "method_not_allowed", "Method not allowed.", "")
			return
		}
		f.paginate(w, r, len(f.members), "data", func(i int) any { return memberJSON(f.members[i]) })
		return
	}
	i, m := f.findMember(rest[0])
	if m == nil {
		apiErr(w, 404, "not_found", "No such member.", "")
		return
	}
	switch r.Method {
	case "GET":
		writeJSON(w, 200, map[string]any{"member": memberJSON(m)})
	case "PATCH":
		body, _, _ := readBody(r)
		role, _ := str(body, "role")
		if m.Role == "owner" || role == "owner" {
			apiErr(w, 403, "owner_protected", ownerMsg, "role")
			return
		}
		if role != "member" {
			apiErr(w, 400, "invalid_request", "role must be member.", "role")
			return
		}
		m.Role = role
		writeJSON(w, 200, map[string]any{"member": memberJSON(m)})
	case "DELETE":
		if m.Role == "owner" {
			apiErr(w, 403, "owner_protected", ownerMsg, "")
			return
		}
		f.members = append(f.members[:i], f.members[i+1:]...)
		writeJSON(w, 200, map[string]any{"member": memberJSON(m)})
	default:
		apiErr(w, 405, "method_not_allowed", "Method not allowed.", "")
	}
}

func invJSON(i *invitation) map[string]any {
	var accepted any
	if i.AcceptedUserID != "" {
		accepted = i.AcceptedUserID
	}
	return map[string]any{"id": i.ID, "email": i.Email, "role": i.Role, "status": i.Status, "accepted_user_id": accepted, "expires_at": ts(i.ExpiresAt), "created_at": ts(i.CreatedAt)}
}

func (f *Fake) invitations(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case "GET":
			f.paginate(w, r, len(f.invites), "data", func(i int) any { return invJSON(f.invites[i]) })
		case "POST":
			m, raw, ok := readBody(r)
			f.idempotent(w, r, raw, func() (int, map[string]any) {
				email, _ := str(m, "email")
				role, has := str(m, "role")
				if !has {
					role = "member"
				}
				email = strings.ToLower(strings.TrimSpace(email))
				if !ok || !strings.Contains(email, "@") {
					return 400, errBody("invalid_request", "email must be an email address.", "email")
				}
				if role == "owner" {
					return 403, errBody("owner_protected", ownerMsg, "role")
				}
				if role != "member" {
					return 400, errBody("invalid_request", "role must be member.", "role")
				}
				for _, mm := range f.members {
					if strings.EqualFold(mm.Email, email) {
						return 409, errBody("conflict", "That person is already a member.", "email")
					}
				}
				for _, ii := range f.invites {
					if ii.Status == "pending" && ii.Email == email {
						return 409, errBody("conflict", email+" already has a pending invitation. Cancel it first, or wait for it to expire.", "email")
					}
				}
				now := time.Now()
				inv := &invitation{ID: f.id(), Email: email, Role: role, Status: "pending", ExpiresAt: now.Add(7 * 24 * time.Hour), CreatedAt: now}
				f.invites = append(f.invites, inv)
				return 201, map[string]any{"invitation": invJSON(inv)}
			})
		default:
			apiErr(w, 405, "method_not_allowed", "Method not allowed.", "")
		}
		return
	}
	for _, inv := range f.invites {
		if inv.ID == rest[0] {
			switch r.Method {
			case "GET":
			case "DELETE":
				if inv.Status == "pending" {
					inv.Status = "cancelled"
				}
			default:
				apiErr(w, 405, "method_not_allowed", "Method not allowed.", "")
				return
			}
			writeJSON(w, 200, map[string]any{"invitation": invJSON(inv)})
			return
		}
	}
	apiErr(w, 404, "not_found", "No such invitation in this organisation.", "")
}

func (f *Fake) tokens(w http.ResponseWriter, r *http.Request, rest []string) {
	maker := map[string]any{"user_id": "u1", "email": "owner@acme.test", "name": "Olive Owner"}
	all := []map[string]any{
		{"id": "00000000-0000-4000-8000-0000000000b1", "name": "terraform", "display": "htd_mgmt_…A1b2", "scopes": []string{"keys:write", "members:write", "settings:write", "sso:write"}, "status": "active", "created_by": maker, "created_at": "2026-11-01T00:00:00Z", "expires_at": nil, "last_used_at": "2026-11-14T00:00:00Z", "revoked_at": nil},
		{"id": "00000000-0000-4000-8000-0000000000b2", "name": "old ci", "display": "htd_mgmt_…Z9y8", "scopes": []string{"keys:read"}, "status": "revoked", "created_by": maker, "created_at": "2026-10-01T00:00:00Z", "expires_at": "2026-12-01T00:00:00Z", "last_used_at": nil, "revoked_at": "2026-10-20T00:00:00Z"},
		{"id": "00000000-0000-4000-8000-0000000000b3", "name": "reader", "display": "htd_mgmt_…Q7w6", "scopes": []string{"keys:read", "members:read"}, "status": "creator_not_owner", "created_by": nil, "created_at": "2026-11-10T00:00:00Z", "expires_at": nil, "last_used_at": nil, "revoked_at": nil},
	}
	if len(rest) == 0 && r.URL.Path == "/v1/management/token" {
		self := map[string]any{}
		for k, v := range all[0] {
			self[k] = v
		}
		self["organization"] = map[string]any{"id": f.orgID, "name": f.orgName}
		writeJSON(w, 200, map[string]any{"token": self})
		return
	}
	if len(rest) == 0 {
		f.paginate(w, r, len(all), "data", func(i int) any { return all[i] })
		return
	}
	for _, t := range all {
		if t["id"] == rest[0] {
			writeJSON(w, 200, map[string]any{"token": t})
			return
		}
	}
	apiErr(w, 404, "not_found", "No such token.", "")
}

func (f *Fake) usage(w http.ResponseWriter, r *http.Request) {
	month := r.URL.Query().Get("month")
	if month == "" {
		month = "2026-11"
	}
	if len(month) != 7 || month[4] != '-' {
		apiErr(w, 400, "invalid_request", "month must be YYYY-MM.", "month")
		return
	}
	writeJSON(w, 200, map[string]any{"usage": map[string]any{
		"month": month, "days": []any{},
		"bill_to_date":    map[string]any{"lines": []any{map[string]any{"meter": "records", "description": "Records", "requests": 12000, "cents": 480}, map[string]any{"meter": "search", "description": "Search", "requests": 300, "cents": 60}}, "total_cents": 540, "requests": 12300},
		"projected_cents": 1800, "sandbox_requests": 42, "spend_cap_cents": f.capCents, "counted_through": "2026-11-14T10:00:00Z",
	}})
}

func (f *Fake) coverage(w http.ResponseWriter, r *http.Request) {
	rows := []map[string]any{
		{"id": "victoria", "kind": "city", "name": "Victoria", "country": "CA", "region": "BC", "meetings": 410, "first_meeting_on": "2019-01-10", "last_meeting_on": "2026-11-13", "read_at": "2026-11-14T06:00:00Z", "meeting_vendors": []string{"escribe"}, "ingest_profile": "full", "held": map[string]int{"meetings": 410, "motions": 1200}},
		{"id": "saanich", "kind": "district", "name": "Saanich", "country": "CA", "region": "BC", "meetings": 220, "first_meeting_on": nil, "last_meeting_on": nil, "read_at": nil, "meeting_vendors": []string{}, "ingest_profile": nil, "held": map[string]int{"meetings": 220}},
		{"id": "seattle", "kind": "city", "name": "Seattle", "country": "US", "region": "WA", "meetings": 90, "first_meeting_on": "2024-01-02", "last_meeting_on": "2026-11-12", "read_at": "2026-11-14T06:00:00Z", "meeting_vendors": []string{"legistar"}, "ingest_profile": "meetings", "held": map[string]int{"meetings": 90}},
	}
	q := r.URL.Query()
	var keep []map[string]any
	for _, row := range rows {
		if v := q.Get("country"); v != "" && row["country"] != v {
			continue
		}
		if v := q.Get("region"); v != "" && row["region"] != v {
			continue
		}
		if v := q.Get("kind"); v != "" && row["kind"] != v {
			continue
		}
		keep = append(keep, row)
	}
	f.paginate(w, r, len(keep), "results", func(i int) any { return keep[i] })
}

// ---- SSO ----

var freeMail = map[string]bool{"gmail.com": true, "outlook.com": true, "yahoo.com": true, "icloud.com": true, "proton.me": true}

func domJSON(d *domain) map[string]any {
	return map[string]any{
		"id": d.ID, "connection_id": d.ConnectionID, "domain": d.Domain,
		"verification": map[string]any{"type": "TXT", "name": "_heytownhall-verify." + d.Domain, "value": "heytownhall-verify=" + d.Token},
		"verified_at":  tsp(d.VerifiedAt), "checked_at": tsp(d.CheckedAt), "created_at": ts(d.CreatedAt),
	}
}

func (f *Fake) connJSON(c *connection) map[string]any {
	ds := []any{}
	for _, d := range c.Domains {
		ds = append(ds, domJSON(d))
	}
	return map[string]any{
		"id": c.ID, "name": c.Name, "protocol": "oidc", "issuer": c.Issuer,
		"client_id": c.ClientID, "client_secret_set": true, "client_secret_updated_at": nil, "email_claim": c.EmailClaim, "trust_unverified_email": c.Trust,
		"enabled": c.Enabled, "enforced": c.Enforced, "redirect_uri": "https://data.heytownhall.com/api/auth/sso/callback",
		"domains": ds, "created_at": ts(c.CreatedAt), "updated_at": ts(c.CreatedAt),
	}
}

func (f *Fake) findConn(id string) *connection {
	for _, c := range f.conns {
		if c.ID == id {
			return c
		}
	}
	return nil
}

func (c *connection) hasVerified() bool {
	for _, d := range c.Domains {
		if d.VerifiedAt != nil {
			return true
		}
	}
	return false
}

func (f *Fake) sso(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case "GET":
			f.paginate(w, r, len(f.conns), "data", func(i int) any { return f.connJSON(f.conns[i]) })
		case "POST":
			m, raw, ok := readBody(r)
			f.idempotent(w, r, raw, func() (int, map[string]any) {
				name, _ := str(m, "name")
				issuer, _ := str(m, "issuer")
				cid, _ := str(m, "client_id")
				sec, _ := str(m, "client_secret")
				claim, trust := "email", false
				if _, has := m["email_claim"]; has {
					return 403, errBody("forbidden", "email_claim can be changed only by an owner in the dashboard.", "email_claim")
				}
				if _, has := m["trust_unverified_email"]; has {
					return 403, errBody("forbidden", "trust_unverified_email can be changed only by an owner in the dashboard.", "trust_unverified_email")
				}
				switch {
				case !ok || name == "":
					return 400, errBody("invalid_request", "name must not be empty.", "name")
				case !strings.HasPrefix(issuer, "https://"):
					return 400, errBody("discovery_failed", "The issuer must be an https URL.", "issuer")
				case cid == "":
					return 400, errBody("invalid_request", "client_id must not be empty.", "client_id")
				case sec == "":
					return 400, errBody("invalid_request", "client_secret must not be empty.", "client_secret")
				case len(f.conns) >= 5:
					return 409, errBody("conflict", "An organisation may hold up to 5 connections.", "")
				}
				c := &connection{ID: f.id(), Name: name, Issuer: issuer, ClientID: cid, Secret: sec, EmailClaim: claim, Trust: trust, CreatedAt: time.Now().UTC()}
				f.conns = append(f.conns, c)
				return 201, map[string]any{"connection": f.connJSON(c)}
			})
		default:
			apiErr(w, 405, "method_not_allowed", "Method not allowed.", "")
		}
		return
	}
	c := f.findConn(rest[0])
	if c == nil {
		apiErr(w, 404, "not_found", "No such connection.", "")
		return
	}
	if len(rest) >= 2 && rest[1] == "domains" {
		f.domains(w, r, c, rest[2:])
		return
	}
	switch r.Method {
	case "GET":
		writeJSON(w, 200, map[string]any{"connection": f.connJSON(c)})
	case "PATCH":
		m, _, _ := readBody(r)
		enabled, enforced := c.Enabled, c.Enforced
		if v, ok := m["enabled"].(bool); ok {
			enabled = v
		}
		if v, ok := m["enforced"].(bool); ok {
			enforced = v
		}
		if enabled && !c.Enabled && !c.hasVerified() {
			apiErr(w, 409, "domain_not_verified", "Verify at least one domain before enabling the connection.", "enabled")
			return
		}
		if b, ok := m["enforced"].(bool); ok && b && !enabled {
			apiErr(w, 409, "conflict", "Enable the connection before enforcing it.", "enforced")
			return
		}
		if v, ok := str(m, "name"); ok {
			c.Name = v
		}
		if v, ok := str(m, "client_id"); ok {
			c.ClientID = v
		}
		if v, ok := str(m, "client_secret"); ok {
			c.Secret = v
		}
		for _, k := range []string{"email_claim", "trust_unverified_email"} {
			if _, has := m[k]; has {
				apiErr(w, 403, "forbidden", k+" can be changed only by an owner in the dashboard.", k)
				return
			}
		}
		c.Enabled, c.Enforced = enabled, enabled && enforced
		writeJSON(w, 200, map[string]any{"connection": f.connJSON(c)})
	case "DELETE":
		for i, x := range f.conns {
			if x == c {
				f.conns = append(f.conns[:i], f.conns[i+1:]...)
			}
		}
		writeJSON(w, 200, map[string]any{"connection": f.connJSON(c)})
	default:
		apiErr(w, 405, "method_not_allowed", "Method not allowed.", "")
	}
}

func (f *Fake) domains(w http.ResponseWriter, r *http.Request, c *connection, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case "GET":
			f.paginate(w, r, len(c.Domains), "data", func(i int) any { return domJSON(c.Domains[i]) })
		case "POST":
			m, raw, ok := readBody(r)
			f.idempotent(w, r, raw, func() (int, map[string]any) {
				d, _ := str(m, "domain")
				d = strings.ToLower(d)
				if !ok || !strings.Contains(d, ".") || strings.ContainsAny(d, "/: @") {
					return 400, errBody("domain_refused", "That is not a domain name.", "domain")
				}
				if freeMail[d] {
					return 400, errBody("domain_refused", "Free-mail domains cannot be claimed.", "domain")
				}
				for _, cc := range f.conns {
					for _, dd := range cc.Domains {
						if dd.Domain == d && dd.VerifiedAt != nil {
							return 409, errBody("domain_taken", "Another organisation has verified that domain.", "domain")
						}
						if dd.Domain == d && cc == c {
							return 409, errBody("conflict", "That domain is already on this connection.", "domain")
						}
					}
				}
				id := f.id()
				dom := &domain{ID: id, ConnectionID: c.ID, Domain: d, Token: strings.Repeat(id[len(id)-4:], 8), CreatedAt: time.Now().UTC()}
				c.Domains = append(c.Domains, dom)
				return 201, map[string]any{"domain": domJSON(dom)}
			})
		default:
			apiErr(w, 405, "method_not_allowed", "Method not allowed.", "")
		}
		return
	}
	var dom *domain
	idx := -1
	for i, d := range c.Domains {
		if d.ID == rest[0] {
			dom, idx = d, i
		}
	}
	if dom == nil {
		apiErr(w, 404, "not_found", "No such domain.", "")
		return
	}
	if len(rest) == 2 && rest[1] == "verify" && r.Method == "POST" {
		n := time.Now().UTC()
		dom.CheckedAt = &n
		f.checks[dom.ID]++
		if dom.VerifiedAt == nil {
			if f.propagateAfter >= 0 && f.checks[dom.ID] > f.propagateAfter {
				f.dns["_heytownhall-verify."+dom.Domain] = "heytownhall-verify=" + dom.Token
			}
			if f.dns["_heytownhall-verify."+dom.Domain] != "heytownhall-verify="+dom.Token {
				apiErr(w, 409, "domain_not_verified", "The TXT record _heytownhall-verify."+dom.Domain+" was not found with the expected value.", "")
				return
			}
			dom.VerifiedAt = &n
		}
		writeJSON(w, 200, map[string]any{"domain": domJSON(dom)})
		return
	}
	switch r.Method {
	case "GET":
		writeJSON(w, 200, map[string]any{"domain": domJSON(dom)})
	case "DELETE":
		c.Domains = append(c.Domains[:idx], c.Domains[idx+1:]...)
		if !c.hasVerified() {
			c.Enabled, c.Enforced = false, false
		}
		writeJSON(w, 200, map[string]any{"domain": domJSON(dom)})
	default:
		apiErr(w, 405, "method_not_allowed", "Method not allowed.", "")
	}
}

// VerifyChecks says how many times a domain has been checked.
func (f *Fake) VerifyChecks(domainName string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.conns {
		for _, d := range c.Domains {
			if d.Domain == domainName {
				return f.checks[d.ID]
			}
		}
	}
	return 0
}
