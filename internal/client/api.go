package client

import (
	"context"
	"net/http"
	"net/url"
)

const base = "/v1/management"

// Organization is the singleton organisation.
type Organization struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	SpendCapCents *int64 `json:"spend_cap_cents"`
	CreatedAt     string `json:"created_at"`
}

// SpendCap is the monthly spending cap. A nil cap means no cap.
type SpendCap struct {
	OrganizationID string `json:"organization_id"`
	Cents          *int64 `json:"spend_cap_cents"`
}

// APIKey is a data API key. The secret is never part of it.
type APIKey struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Kind       string  `json:"kind"`
	Display    string  `json:"display"`
	CreatedAt  string  `json:"created_at"`
	LastUsedAt *string `json:"last_used_at"`
	RevokedAt  *string `json:"revoked_at"`
}

// Member is a person in the organisation.
type Member struct {
	UserID   string `json:"user_id"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	JoinedAt string `json:"joined_at"`
}

// Invitation is a pending invitation.
type Invitation struct {
	ID             string  `json:"id"`
	Email          string  `json:"email"`
	Role           string  `json:"role"`
	Status         string  `json:"status"` // pending, accepted, expired, cancelled or rejected
	AcceptedUserID *string `json:"accepted_user_id"`
	ExpiresAt      string  `json:"expires_at"`
	CreatedAt      string  `json:"created_at"`
}

// DomainVerification is the DNS record that proves control of a domain.
type DomainVerification struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

// SSODomain is an email domain claimed by a connection.
type SSODomain struct {
	ID           string              `json:"id"`
	ConnectionID string              `json:"connection_id"`
	Domain       string              `json:"domain"`
	Verification *DomainVerification `json:"verification"`
	VerifiedAt   *string             `json:"verified_at"`
	CheckedAt    *string             `json:"checked_at"`
	CreatedAt    string              `json:"created_at"`
}

// SSOConnection is an OpenID Connect connection. The client secret is never returned.
type SSOConnection struct {
	ID                   string      `json:"id"`
	Name                 string      `json:"name"`
	Protocol             string      `json:"protocol"`
	Issuer               string      `json:"issuer"`
	ClientID             string      `json:"client_id"`
	ClientSecretSet      bool        `json:"client_secret_set"`
	ClientSecretUpdated  *string     `json:"client_secret_updated_at"`
	EmailClaim           string      `json:"email_claim"`
	TrustUnverifiedEmail bool        `json:"trust_unverified_email"`
	Enabled              bool        `json:"enabled"`
	Enforced             bool        `json:"enforced"`
	RedirectURI          string      `json:"redirect_uri"`
	Domains              []SSODomain `json:"domains"`
}

// SSOConnectionCreate is the body of a create.
type SSOConnectionCreate struct {
	Name         string `json:"name"`
	Issuer       string `json:"issuer"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

// SSOConnectionUpdate is the body of an update. A nil field is not sent.
type SSOConnectionUpdate struct {
	Name         *string `json:"name,omitempty"`
	ClientID     *string `json:"client_id,omitempty"`
	ClientSecret *string `json:"client_secret,omitempty"`
	Enabled      *bool   `json:"enabled,omitempty"`
	Enforced     *bool   `json:"enforced,omitempty"`
}

// ManagementToken is a token's metadata.
type ManagementToken struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	Display    string      `json:"display"`
	Scopes     []string    `json:"scopes"`
	Status     string      `json:"status"` // active, expired, revoked or creator_not_owner
	CreatedBy  *TokenMaker `json:"created_by"`
	CreatedAt  string      `json:"created_at"`
	ExpiresAt  *string     `json:"expires_at"`
	LastUsedAt *string     `json:"last_used_at"`
	RevokedAt  *string     `json:"revoked_at"`
	// Organization is set on the answer to GET /token only.
	Organization *Organization `json:"organization"`
}

// TokenMaker is the owner who made a token.
type TokenMaker struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Name   string `json:"name"`
}

// BillLine is one line of the bill to date.
type BillLine struct {
	Meter       string `json:"meter"`
	Description string `json:"description"`
	Requests    int64  `json:"requests"`
	Cents       int64  `json:"cents"`
}

// Usage is a month's usage and bill to date.
type Usage struct {
	Month      string `json:"month"`
	BillToDate struct {
		Lines      []BillLine `json:"lines"`
		TotalCents int64      `json:"total_cents"`
		Requests   int64      `json:"requests"`
	} `json:"bill_to_date"`
	ProjectedCents  *int64  `json:"projected_cents"`
	SandboxRequests int64   `json:"sandbox_requests"`
	SpendCapCents   *int64  `json:"spend_cap_cents"`
	CountedThrough  *string `json:"counted_through"`
}

// CoverageRow is one jurisdiction in the public coverage list.
type CoverageRow struct {
	ID             string           `json:"id"`
	Kind           string           `json:"kind"`
	Name           string           `json:"name"`
	Country        *string          `json:"country"`
	Region         *string          `json:"region"`
	Meetings       *int64           `json:"meetings"`
	FirstMeetingOn *string          `json:"first_meeting_on"`
	LastMeetingOn  *string          `json:"last_meeting_on"`
	ReadAt         *string          `json:"read_at"`
	MeetingVendors []string         `json:"meeting_vendors"`
	IngestProfile  *string          `json:"ingest_profile"`
	Held           map[string]int64 `json:"held"`
}

func pathEscape(s string) string { return url.PathEscape(s) }

// GetOrganization reads the organisation.
func (c *Client) GetOrganization(ctx context.Context) (*Organization, error) {
	var o Organization
	if err := c.call(ctx, http.MethodGet, base+"/organization", reqOpts{}, "organization", &o); err != nil {
		return nil, err
	}
	return &o, nil
}

// UpdateOrganization renames the organisation.
func (c *Client) UpdateOrganization(ctx context.Context, name string) (*Organization, error) {
	var o Organization
	if err := c.call(ctx, http.MethodPatch, base+"/organization", reqOpts{body: map[string]any{"name": name}}, "organization", &o); err != nil {
		return nil, err
	}
	return &o, nil
}

// GetSpendCap reads the cap.
func (c *Client) GetSpendCap(ctx context.Context) (*SpendCap, error) {
	var s SpendCap
	if err := c.call(ctx, http.MethodGet, base+"/spend-cap", reqOpts{}, "spend_cap", &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// PutSpendCap sets the cap; nil removes it.
func (c *Client) PutSpendCap(ctx context.Context, cents *int64) (*SpendCap, error) {
	var s SpendCap
	if err := c.call(ctx, http.MethodPut, base+"/spend-cap", reqOpts{body: map[string]any{"spend_cap_cents": cents}}, "spend_cap", &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// ListAPIKeys lists every key, revoked ones included.
func (c *Client) ListAPIKeys(ctx context.Context) ([]APIKey, error) {
	return listAll[APIKey](ctx, c, base+"/api-keys", url.Values{"include_revoked": {"true"}}, false)
}

// GetToken reads the management token the provider is using.
func (c *Client) GetToken(ctx context.Context) (*ManagementToken, error) {
	var t ManagementToken
	if err := c.call(ctx, http.MethodGet, base+"/token", reqOpts{}, "token", &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// CreateAPIKey creates a key. The secret is nil when the API answered an idempotent replay.
func (c *Client) CreateAPIKey(ctx context.Context, name, kind string) (*APIKey, *string, error) {
	var out struct {
		Key    APIKey  `json:"key"`
		Secret *string `json:"secret"`
	}
	if err := c.call(ctx, http.MethodPost, base+"/api-keys", reqOpts{body: map[string]any{"name": name, "kind": kind}, idempotent: true}, "", &out); err != nil {
		return nil, nil, err
	}
	return &out.Key, out.Secret, nil
}

// GetAPIKey reads a key. A revoked key comes back with revoked_at set.
func (c *Client) GetAPIKey(ctx context.Context, id string) (*APIKey, error) {
	var k APIKey
	if err := c.call(ctx, http.MethodGet, base+"/api-keys/"+pathEscape(id), reqOpts{}, "key", &k); err != nil {
		return nil, err
	}
	return &k, nil
}

// UpdateAPIKey renames a key.
func (c *Client) UpdateAPIKey(ctx context.Context, id, name string) (*APIKey, error) {
	var k APIKey
	if err := c.call(ctx, http.MethodPatch, base+"/api-keys/"+pathEscape(id), reqOpts{body: map[string]any{"name": name}}, "key", &k); err != nil {
		return nil, err
	}
	return &k, nil
}

// RevokeAPIKey revokes a key. Revoking twice is not an error.
func (c *Client) RevokeAPIKey(ctx context.Context, id string) error {
	return c.call(ctx, http.MethodDelete, base+"/api-keys/"+pathEscape(id), reqOpts{}, "", nil)
}

// ListMembers lists the members.
func (c *Client) ListMembers(ctx context.Context) ([]Member, error) {
	return listAll[Member](ctx, c, base+"/members", nil, false)
}

// GetMember reads a member by user id.
func (c *Client) GetMember(ctx context.Context, userID string) (*Member, error) {
	var m Member
	if err := c.call(ctx, http.MethodGet, base+"/members/"+pathEscape(userID), reqOpts{}, "member", &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// UpdateMember changes a member's role.
func (c *Client) UpdateMember(ctx context.Context, userID, role string) (*Member, error) {
	var m Member
	if err := c.call(ctx, http.MethodPatch, base+"/members/"+pathEscape(userID), reqOpts{body: map[string]any{"role": role}}, "member", &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// DeleteMember removes a member from the organisation.
func (c *Client) DeleteMember(ctx context.Context, userID string) error {
	return c.call(ctx, http.MethodDelete, base+"/members/"+pathEscape(userID), reqOpts{}, "", nil)
}

// CreateInvitation invites an email address.
func (c *Client) CreateInvitation(ctx context.Context, email, role string) (*Invitation, error) {
	var i Invitation
	if err := c.call(ctx, http.MethodPost, base+"/invitations", reqOpts{body: map[string]any{"email": email, "role": role}, idempotent: true}, "invitation", &i); err != nil {
		return nil, err
	}
	return &i, nil
}

// GetInvitation reads an invitation.
func (c *Client) GetInvitation(ctx context.Context, id string) (*Invitation, error) {
	var i Invitation
	if err := c.call(ctx, http.MethodGet, base+"/invitations/"+pathEscape(id), reqOpts{}, "invitation", &i); err != nil {
		return nil, err
	}
	return &i, nil
}

// CancelInvitation cancels an invitation.
func (c *Client) CancelInvitation(ctx context.Context, id string) error {
	return c.call(ctx, http.MethodDelete, base+"/invitations/"+pathEscape(id), reqOpts{}, "", nil)
}

// CreateSSOConnection creates a connection.
func (c *Client) CreateSSOConnection(ctx context.Context, in SSOConnectionCreate) (*SSOConnection, error) {
	var s SSOConnection
	if err := c.call(ctx, http.MethodPost, base+"/sso-connections", reqOpts{body: in, idempotent: true}, "connection", &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// GetSSOConnection reads a connection.
func (c *Client) GetSSOConnection(ctx context.Context, id string) (*SSOConnection, error) {
	var s SSOConnection
	if err := c.call(ctx, http.MethodGet, base+"/sso-connections/"+pathEscape(id), reqOpts{}, "connection", &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// UpdateSSOConnection changes a connection.
func (c *Client) UpdateSSOConnection(ctx context.Context, id string, in SSOConnectionUpdate) (*SSOConnection, error) {
	var s SSOConnection
	if err := c.call(ctx, http.MethodPatch, base+"/sso-connections/"+pathEscape(id), reqOpts{body: in}, "connection", &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// DeleteSSOConnection deletes a connection.
func (c *Client) DeleteSSOConnection(ctx context.Context, id string) error {
	return c.call(ctx, http.MethodDelete, base+"/sso-connections/"+pathEscape(id), reqOpts{}, "", nil)
}

func domainsPath(connectionID string) string {
	return base + "/sso-connections/" + pathEscape(connectionID) + "/domains"
}

// CreateSSODomain claims a domain and returns the DNS record to publish.
func (c *Client) CreateSSODomain(ctx context.Context, connectionID, domain string) (*SSODomain, error) {
	var d SSODomain
	if err := c.call(ctx, http.MethodPost, domainsPath(connectionID), reqOpts{body: map[string]any{"domain": domain}, idempotent: true}, "domain", &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// GetSSODomain reads a domain.
func (c *Client) GetSSODomain(ctx context.Context, connectionID, domainID string) (*SSODomain, error) {
	var d SSODomain
	if err := c.call(ctx, http.MethodGet, domainsPath(connectionID)+"/"+pathEscape(domainID), reqOpts{}, "domain", &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// VerifySSODomain asks the API to look for the DNS record now.
func (c *Client) VerifySSODomain(ctx context.Context, connectionID, domainID string) (*SSODomain, error) {
	var d SSODomain
	if err := c.call(ctx, http.MethodPost, domainsPath(connectionID)+"/"+pathEscape(domainID)+"/verify", reqOpts{}, "domain", &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// DeleteSSODomain removes a domain.
func (c *Client) DeleteSSODomain(ctx context.Context, connectionID, domainID string) error {
	return c.call(ctx, http.MethodDelete, domainsPath(connectionID)+"/"+pathEscape(domainID), reqOpts{}, "", nil)
}

// ListManagementTokens lists token metadata.
func (c *Client) ListManagementTokens(ctx context.Context) ([]ManagementToken, error) {
	return listAll[ManagementToken](ctx, c, base+"/management-tokens", nil, false)
}

// GetUsage reads a month's usage. An empty month means the current month.
func (c *Client) GetUsage(ctx context.Context, month string) (*Usage, error) {
	q := url.Values{}
	if month != "" {
		q.Set("month", month)
	}
	var u Usage
	if err := c.call(ctx, http.MethodGet, base+"/usage", reqOpts{query: q}, "usage", &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// ListCoverage reads the public coverage list. It sends no token: data routes do not accept one.
func (c *Client) ListCoverage(ctx context.Context, region, country, kind string) ([]CoverageRow, error) {
	q := url.Values{}
	for k, v := range map[string]string{"region": region, "country": country, "kind": kind} {
		if v != "" {
			q.Set(k, v)
		}
	}
	return listAll[CoverageRow](ctx, c, "/v1/coverage", q, true)
}
