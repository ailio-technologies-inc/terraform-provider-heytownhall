package provider

import (
	"context"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ailio-technologies-inc/terraform-provider-heytownhall/internal/client"
)

// Every data source reads the client the same way.
type baseDataSource struct {
	c    *client.Client
	name string
}

func (d *baseDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + d.name
}

func (d *baseDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.c = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func strs(in []string) []types.String {
	out := make([]types.String, 0, len(in))
	for _, s := range in {
		out = append(out, types.StringValue(s))
	}
	return out
}

// ---- organization ----

// NewOrganizationDataSource is the heytownhall_organization data source.
func NewOrganizationDataSource() datasource.DataSource {
	return &organizationDataSource{baseDataSource{name: "organization"}}
}

type organizationDataSource struct{ baseDataSource }

func (d *organizationDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The organisation the management token belongs to.",
		Attributes: map[string]schema.Attribute{
			"id":              schema.StringAttribute{Computed: true, Description: "The organisation's id."},
			"name":            schema.StringAttribute{Computed: true, Description: "The organisation's name."},
			"spend_cap_cents": schema.Int64Attribute{Computed: true, Description: "The monthly spending cap in US cents, or null for no cap."},
		},
	}
}

func (d *organizationDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	org, err := d.c.GetOrganization(ctx)
	if err != nil {
		apiDiag(&resp.Diagnostics, "Could not read the organisation", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, struct {
		ID    types.String `tfsdk:"id"`
		Name  types.String `tfsdk:"name"`
		Cents types.Int64  `tfsdk:"spend_cap_cents"`
	}{types.StringValue(org.ID), types.StringValue(org.Name), types.Int64PointerValue(org.SpendCapCents)})...)
}

// ---- usage ----

// NewUsageDataSource is the heytownhall_usage data source.
func NewUsageDataSource() datasource.DataSource {
	return &usageDataSource{baseDataSource{name: "usage"}}
}

type usageDataSource struct{ baseDataSource }

type billLineModel struct {
	Meter       types.String `tfsdk:"meter"`
	Description types.String `tfsdk:"description"`
	Requests    types.Int64  `tfsdk:"requests"`
	Cents       types.Int64  `tfsdk:"cents"`
}

type usageModel struct {
	Month           types.String    `tfsdk:"month"`
	TotalCents      types.Int64     `tfsdk:"total_cents"`
	Requests        types.Int64     `tfsdk:"requests"`
	SandboxRequests types.Int64     `tfsdk:"sandbox_requests"`
	ProjectedCents  types.Int64     `tfsdk:"projected_cents"`
	SpendCapCents   types.Int64     `tfsdk:"spend_cap_cents"`
	CountedThrough  types.String    `tfsdk:"counted_through"`
	Lines           []billLineModel `tfsdk:"lines"`
}

func (d *usageDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A month's live usage and the bill to date. Counts reach the database every few minutes, so the newest requests may be missing; `counted_through` says how far the count goes.",
		Attributes: map[string]schema.Attribute{
			"month": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "The month as `YYYY-MM`, in Vancouver time. Defaults to the current month.",
				Validators:  []validator.String{stringvalidator.RegexMatches(regexp.MustCompile(`^[0-9]{4}-(0[1-9]|1[0-2])$`), "must be a month written YYYY-MM")},
			},
			"total_cents":      schema.Int64Attribute{Computed: true, Description: "The bill to date in US cents."},
			"requests":         schema.Int64Attribute{Computed: true, Description: "Billed live requests so far."},
			"sandbox_requests": schema.Int64Attribute{Computed: true, Description: "Sandbox requests so far. They are not billed."},
			"projected_cents":  schema.Int64Attribute{Computed: true, Description: "The bill at this month's pace, to its last day. Null before any live request."},
			"spend_cap_cents":  schema.Int64Attribute{Computed: true, Description: "The spending cap in US cents, or null for no cap."},
			"counted_through":  schema.StringAttribute{Computed: true, Description: "The time up to which requests are counted (RFC 3339)."},
			"lines": schema.ListNestedAttribute{
				Computed:    true,
				Description: "The bill by meter.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"meter":       schema.StringAttribute{Computed: true, Description: "The meter's name."},
					"description": schema.StringAttribute{Computed: true, Description: "What the line bills."},
					"requests":    schema.Int64Attribute{Computed: true, Description: "Requests on this meter."},
					"cents":       schema.Int64Attribute{Computed: true, Description: "The line's amount in US cents."},
				}},
			},
		},
	}
}

func (d *usageDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg usageModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	u, err := d.c.GetUsage(ctx, cfg.Month.ValueString())
	if err != nil {
		apiDiag(&resp.Diagnostics, "Could not read usage", err)
		return
	}
	m := usageModel{
		Month:           types.StringValue(u.Month),
		TotalCents:      types.Int64Value(u.BillToDate.TotalCents),
		Requests:        types.Int64Value(u.BillToDate.Requests),
		SandboxRequests: types.Int64Value(u.SandboxRequests),
		ProjectedCents:  types.Int64PointerValue(u.ProjectedCents),
		SpendCapCents:   types.Int64PointerValue(u.SpendCapCents),
		CountedThrough:  types.StringPointerValue(u.CountedThrough),
		Lines:           []billLineModel{},
	}
	for _, l := range u.BillToDate.Lines {
		m.Lines = append(m.Lines, billLineModel{types.StringValue(l.Meter), types.StringValue(l.Description), types.Int64Value(l.Requests), types.Int64Value(l.Cents)})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

// ---- api keys ----

// NewAPIKeysDataSource is the heytownhall_api_keys data source.
func NewAPIKeysDataSource() datasource.DataSource {
	return &apiKeysDataSource{baseDataSource{name: "api_keys"}}
}

type apiKeysDataSource struct{ baseDataSource }

type apiKeyItem struct {
	ID         types.String `tfsdk:"id"`
	Name       types.String `tfsdk:"name"`
	Kind       types.String `tfsdk:"kind"`
	Display    types.String `tfsdk:"display"`
	CreatedAt  types.String `tfsdk:"created_at"`
	LastUsedAt types.String `tfsdk:"last_used_at"`
	RevokedAt  types.String `tfsdk:"revoked_at"`
}

type apiKeysModel struct {
	IncludeRevoked types.Bool   `tfsdk:"include_revoked"`
	Keys           []apiKeyItem `tfsdk:"keys"`
}

func (d *apiKeysDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The organisation's API keys. Secrets are never part of the answer.",
		Attributes: map[string]schema.Attribute{
			"include_revoked": schema.BoolAttribute{Optional: true, Description: "Also list revoked keys. Defaults to false."},
			"keys": schema.ListNestedAttribute{
				Computed:    true,
				Description: "The keys, oldest first.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":           schema.StringAttribute{Computed: true, Description: "The key's id."},
					"name":         schema.StringAttribute{Computed: true, Description: "The key's name."},
					"kind":         schema.StringAttribute{Computed: true, Description: "`sandbox` or `live`."},
					"display":      schema.StringAttribute{Computed: true, Description: "The key's prefix and last four characters."},
					"created_at":   schema.StringAttribute{Computed: true, Description: "When the key was created (RFC 3339)."},
					"last_used_at": schema.StringAttribute{Computed: true, Description: "When the key was last used (RFC 3339), or null."},
					"revoked_at":   schema.StringAttribute{Computed: true, Description: "When the key was revoked (RFC 3339), or null."},
				}},
			},
		},
	}
}

func (d *apiKeysDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg apiKeysModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	keys, err := d.c.ListAPIKeys(ctx)
	if err != nil {
		apiDiag(&resp.Diagnostics, "Could not list API keys", err)
		return
	}
	out := apiKeysModel{IncludeRevoked: cfg.IncludeRevoked, Keys: []apiKeyItem{}}
	for _, k := range keys {
		if k.RevokedAt != nil && !cfg.IncludeRevoked.ValueBool() {
			continue
		}
		out.Keys = append(out.Keys, apiKeyItem{types.StringValue(k.ID), types.StringValue(k.Name), types.StringValue(k.Kind), types.StringValue(k.Display),
			types.StringValue(k.CreatedAt), types.StringPointerValue(k.LastUsedAt), types.StringPointerValue(k.RevokedAt)})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, out)...)
}

// ---- members ----

// NewMembersDataSource is the heytownhall_members data source.
func NewMembersDataSource() datasource.DataSource {
	return &membersDataSource{baseDataSource{name: "members"}}
}

type membersDataSource struct{ baseDataSource }

type memberItem struct {
	UserID   types.String `tfsdk:"user_id"`
	Email    types.String `tfsdk:"email"`
	Name     types.String `tfsdk:"name"`
	Role     types.String `tfsdk:"role"`
	JoinedAt types.String `tfsdk:"joined_at"`
}

type membersModel struct {
	Members []memberItem `tfsdk:"members"`
}

func (d *membersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The organisation's members, owners included. Use it to find a person's `user_id` by email for `heytownhall_member`.",
		Attributes: map[string]schema.Attribute{
			"members": schema.ListNestedAttribute{
				Computed:    true,
				Description: "The members.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"user_id":   schema.StringAttribute{Computed: true, Description: "The user id."},
					"email":     schema.StringAttribute{Computed: true, Description: "The email address."},
					"name":      schema.StringAttribute{Computed: true, Description: "The name."},
					"role":      schema.StringAttribute{Computed: true, Description: "`owner` or `member`."},
					"joined_at": schema.StringAttribute{Computed: true, Description: "When the person joined (RFC 3339)."},
				}},
			},
		},
	}
}

func (d *membersDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	ms, err := d.c.ListMembers(ctx)
	if err != nil {
		apiDiag(&resp.Diagnostics, "Could not list members", err)
		return
	}
	out := membersModel{Members: []memberItem{}}
	for _, m := range ms {
		out.Members = append(out.Members, memberItem{types.StringValue(m.UserID), types.StringValue(m.Email), types.StringValue(m.Name), types.StringValue(m.Role), types.StringValue(m.JoinedAt)})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, out)...)
}

// ---- management tokens ----

// NewManagementTokensDataSource is the heytownhall_management_tokens data source.
func NewManagementTokensDataSource() datasource.DataSource {
	return &managementTokensDataSource{baseDataSource{name: "management_tokens"}}
}

type managementTokensDataSource struct{ baseDataSource }

type tokenItem struct {
	ID         types.String   `tfsdk:"id"`
	Name       types.String   `tfsdk:"name"`
	Display    types.String   `tfsdk:"display"`
	Status     types.String   `tfsdk:"status"`
	CreatedBy  types.String   `tfsdk:"created_by"`
	Scopes     []types.String `tfsdk:"scopes"`
	CreatedAt  types.String   `tfsdk:"created_at"`
	ExpiresAt  types.String   `tfsdk:"expires_at"`
	LastUsedAt types.String   `tfsdk:"last_used_at"`
	RevokedAt  types.String   `tfsdk:"revoked_at"`
}

type tokensModel struct {
	Tokens []tokenItem `tfsdk:"tokens"`
}

func (d *managementTokensDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The organisation's management tokens: names, scopes and dates, never the tokens themselves. Tokens are made and revoked in the dashboard, not through the API.",
		Attributes: map[string]schema.Attribute{
			"tokens": schema.ListNestedAttribute{
				Computed:    true,
				Description: "The tokens.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":           schema.StringAttribute{Computed: true, Description: "The token's id."},
					"name":         schema.StringAttribute{Computed: true, Description: "The token's name."},
					"display":      schema.StringAttribute{Computed: true, Description: "The token's prefix and last four characters."},
					"status":       schema.StringAttribute{Computed: true, Description: "`active`, `expired`, `revoked`, or `creator_not_owner` (the owner who made the token is no longer an owner, so the token no longer works)."},
					"created_by":   schema.StringAttribute{Computed: true, Description: "The email address of the owner who made the token."},
					"scopes":       schema.ListAttribute{Computed: true, ElementType: types.StringType, Description: "The scopes, such as `keys:write`."},
					"created_at":   schema.StringAttribute{Computed: true, Description: "When the token was created (RFC 3339)."},
					"expires_at":   schema.StringAttribute{Computed: true, Description: "When the token expires (RFC 3339), or null."},
					"last_used_at": schema.StringAttribute{Computed: true, Description: "When the token was last used (RFC 3339), or null."},
					"revoked_at":   schema.StringAttribute{Computed: true, Description: "When the token was revoked (RFC 3339), or null."},
				}},
			},
		},
	}
}

func (d *managementTokensDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	ts, err := d.c.ListManagementTokens(ctx)
	if err != nil {
		apiDiag(&resp.Diagnostics, "Could not list management tokens", err)
		return
	}
	out := tokensModel{Tokens: []tokenItem{}}
	for _, t := range ts {
		out.Tokens = append(out.Tokens, tokenItem{types.StringValue(t.ID), types.StringValue(t.Name), types.StringValue(t.Display), types.StringValue(t.Status), maker(t), strs(t.Scopes),
			types.StringValue(t.CreatedAt), types.StringPointerValue(t.ExpiresAt), types.StringPointerValue(t.LastUsedAt), types.StringPointerValue(t.RevokedAt)})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, out)...)
}

// ---- coverage ----

// NewCoverageDataSource is the heytownhall_coverage data source.
func NewCoverageDataSource() datasource.DataSource {
	return &coverageDataSource{baseDataSource{name: "coverage"}}
}

type coverageDataSource struct{ baseDataSource }

type coverageItem struct {
	ID             types.String           `tfsdk:"id"`
	Kind           types.String           `tfsdk:"kind"`
	Name           types.String           `tfsdk:"name"`
	Country        types.String           `tfsdk:"country"`
	Region         types.String           `tfsdk:"region"`
	Meetings       types.Int64            `tfsdk:"meetings"`
	FirstMeetingOn types.String           `tfsdk:"first_meeting_on"`
	LastMeetingOn  types.String           `tfsdk:"last_meeting_on"`
	ReadAt         types.String           `tfsdk:"read_at"`
	MeetingVendors []types.String         `tfsdk:"meeting_vendors"`
	IngestProfile  types.String           `tfsdk:"ingest_profile"`
	Held           map[string]types.Int64 `tfsdk:"held"`
}

type coverageModel struct {
	Country       types.String   `tfsdk:"country"`
	Region        types.String   `tfsdk:"region"`
	Kind          types.String   `tfsdk:"kind"`
	Jurisdictions []coverageItem `tfsdk:"jurisdictions"`
}

func (d *coverageDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The public coverage list: each jurisdiction the product reads, how much of it, and what is held now. " +
			"It comes from `GET /v1/coverage`, which needs no key and is not billed, so the data source works with any valid provider configuration.",
		Attributes: map[string]schema.Attribute{
			"country": schema.StringAttribute{Optional: true, Description: "Only jurisdictions in this country (a two-letter code such as `CA`)."},
			"region":  schema.StringAttribute{Optional: true, Description: "Only jurisdictions in this region (such as `BC`)."},
			"kind":    schema.StringAttribute{Optional: true, Description: "Only jurisdictions of this kind (such as `city`)."},
			"jurisdictions": schema.ListNestedAttribute{
				Computed:    true,
				Description: "The jurisdictions.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":               schema.StringAttribute{Computed: true, Description: "The jurisdiction's id."},
					"kind":             schema.StringAttribute{Computed: true, Description: "What kind of body it is."},
					"name":             schema.StringAttribute{Computed: true, Description: "Its name."},
					"country":          schema.StringAttribute{Computed: true, Description: "Its country."},
					"region":           schema.StringAttribute{Computed: true, Description: "Its region."},
					"meetings":         schema.Int64Attribute{Computed: true, Description: "Meetings held."},
					"first_meeting_on": schema.StringAttribute{Computed: true, Description: "The date of the earliest meeting."},
					"last_meeting_on":  schema.StringAttribute{Computed: true, Description: "The date of the latest meeting."},
					"read_at":          schema.StringAttribute{Computed: true, Description: "When it was last read (RFC 3339)."},
					"meeting_vendors":  schema.ListAttribute{Computed: true, ElementType: types.StringType, Description: "The meeting platforms it is read from."},
					"ingest_profile":   schema.StringAttribute{Computed: true, Description: "How much of it is read."},
					"held":             schema.MapAttribute{Computed: true, ElementType: types.Int64Type, Description: "Records held now, by family."},
				}},
			},
		},
	}
}

func (d *coverageDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg coverageModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	rows, err := d.c.ListCoverage(ctx, cfg.Region.ValueString(), cfg.Country.ValueString(), cfg.Kind.ValueString())
	if err != nil {
		apiDiag(&resp.Diagnostics, "Could not read coverage", err)
		return
	}
	cfg.Jurisdictions = []coverageItem{}
	for _, r := range rows {
		held := map[string]types.Int64{}
		for k, v := range r.Held {
			held[k] = types.Int64Value(v)
		}
		cfg.Jurisdictions = append(cfg.Jurisdictions, coverageItem{
			ID: types.StringValue(r.ID), Kind: types.StringValue(r.Kind), Name: types.StringValue(r.Name),
			Country: types.StringPointerValue(r.Country), Region: types.StringPointerValue(r.Region),
			Meetings: types.Int64PointerValue(r.Meetings), FirstMeetingOn: types.StringPointerValue(r.FirstMeetingOn),
			LastMeetingOn: types.StringPointerValue(r.LastMeetingOn), ReadAt: types.StringPointerValue(r.ReadAt),
			MeetingVendors: strs(r.MeetingVendors), IngestProfile: types.StringPointerValue(r.IngestProfile), Held: held,
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, cfg)...)
}

func maker(t client.ManagementToken) types.String {
	if t.CreatedBy == nil {
		return types.StringNull()
	}
	return types.StringValue(t.CreatedBy.Email)
}

// ---- token (the one in use) ----

// NewTokenDataSource is the heytownhall_token data source.
func NewTokenDataSource() datasource.DataSource {
	return &tokenDataSource{baseDataSource{name: "token"}}
}

type tokenDataSource struct{ baseDataSource }

type tokenSelfModel struct {
	ID               types.String   `tfsdk:"id"`
	Name             types.String   `tfsdk:"name"`
	Display          types.String   `tfsdk:"display"`
	Status           types.String   `tfsdk:"status"`
	Scopes           []types.String `tfsdk:"scopes"`
	ExpiresAt        types.String   `tfsdk:"expires_at"`
	OrganizationID   types.String   `tfsdk:"organization_id"`
	OrganizationName types.String   `tfsdk:"organization_name"`
}

func (d *tokenDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The management token the provider is using: its scopes, its expiry, and the organisation it belongs to. Useful to check, in a plan, that the token has the scopes the configuration needs.",
		Attributes: map[string]schema.Attribute{
			"id":                schema.StringAttribute{Computed: true, Description: "The token's id."},
			"name":              schema.StringAttribute{Computed: true, Description: "The token's name."},
			"display":           schema.StringAttribute{Computed: true, Description: "The token's prefix and last four characters."},
			"status":            schema.StringAttribute{Computed: true, Description: "`active` for a token that works."},
			"scopes":            schema.ListAttribute{Computed: true, ElementType: types.StringType, Description: "The token's scopes, such as `keys:write`."},
			"expires_at":        schema.StringAttribute{Computed: true, Description: "When the token expires (RFC 3339), or null."},
			"organization_id":   schema.StringAttribute{Computed: true, Description: "The organisation's id."},
			"organization_name": schema.StringAttribute{Computed: true, Description: "The organisation's name."},
		},
	}
}

func (d *tokenDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	t, err := d.c.GetToken(ctx)
	if err != nil {
		apiDiag(&resp.Diagnostics, "Could not read the token", err)
		return
	}
	m := tokenSelfModel{ID: types.StringValue(t.ID), Name: types.StringValue(t.Name), Display: types.StringValue(t.Display), Status: types.StringValue(t.Status),
		Scopes: strs(t.Scopes), ExpiresAt: types.StringPointerValue(t.ExpiresAt), OrganizationID: types.StringNull(), OrganizationName: types.StringNull()}
	if t.Organization != nil {
		m.OrganizationID, m.OrganizationName = types.StringValue(t.Organization.ID), types.StringValue(t.Organization.Name)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}
