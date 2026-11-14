package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ailio-technologies-inc/terraform-provider-heytownhall/internal/client"
)

var (
	_ resource.Resource                = &ssoConnectionResource{}
	_ resource.ResourceWithConfigure   = &ssoConnectionResource{}
	_ resource.ResourceWithImportState = &ssoConnectionResource{}
)

// NewSSOConnectionResource is the heytownhall_sso_connection resource.
func NewSSOConnectionResource() resource.Resource { return &ssoConnectionResource{} }

type ssoConnectionResource struct{ c *client.Client }

type ssoConnectionModel struct {
	ID                    types.String `tfsdk:"id"`
	Name                  types.String `tfsdk:"name"`
	Issuer                types.String `tfsdk:"issuer"`
	ClientID              types.String `tfsdk:"client_id"`
	ClientSecret          types.String `tfsdk:"client_secret"`
	EmailClaim            types.String `tfsdk:"email_claim"`
	TrustUnverifiedEmail  types.Bool   `tfsdk:"trust_unverified_email"`
	RedirectURI           types.String `tfsdk:"redirect_uri"`
	ClientSecretUpdatedAt types.String `tfsdk:"client_secret_updated_at"`
	Discovery             types.Object `tfsdk:"discovery"`
}

var discoveryTypes = map[string]attr.Type{
	"authorization_endpoint": types.StringType, "token_endpoint": types.StringType, "jwks_uri": types.StringType,
	"token_endpoint_auth_method": types.StringType, "refreshed_at": types.StringType,
}

func (r *ssoConnectionResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_sso_connection"
}

func (r *ssoConnectionResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	computed := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		Description: "A single sign-on connection to an OpenID Connect identity provider. An organisation can have up to five.\n\n" +
			"A new connection signs nobody in. Add one or more `heytownhall_sso_domain` resources, publish each domain's DNS record, " +
			"confirm it with `heytownhall_sso_domain_verification`, and then turn the connection on with `heytownhall_sso_activation`. " +
			"Enabling and enforcing sign-in are in `heytownhall_sso_activation`, not here, because they depend on a verified domain and a verified domain depends on this resource.\n\n" +
			"Register `redirect_uri` with your identity provider as the only redirect URI.\n\n" +
			"`email_claim` and `trust_unverified_email` can be changed only by an owner in the dashboard, not by a management token, so they are read-only here.\n\n" +
			"**The client secret is write-only.** The API accepts it and never returns it, so Terraform cannot see whether it was changed elsewhere. " +
			"Terraform sends `client_secret` when the connection is created and whenever the value in the configuration changes. " +
			"A change made to the secret in the dashboard is not detected. After an import, `client_secret` is unknown to Terraform, and the first apply sets it from the configuration.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, Description: "The connection's id.", PlanModifiers: computed},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "A name for the connection, such as `Okta`.",
				Validators:  []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"issuer": schema.StringAttribute{
				Required: true,
				Description: "The identity provider's issuer URL, exactly as its discovery document names it (for example `https://acme.okta.com`). It must use https. " +
					"The API reads `<issuer>/.well-known/openid-configuration` when the connection is created and refuses an issuer that does not match. Changing it replaces the connection.",
				Validators:    []validator.String{httpsURLValidator{}},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"client_id": schema.StringAttribute{
				Required:    true,
				Description: "The client id of the application registered with the identity provider.",
				Validators:  []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"client_secret": schema.StringAttribute{
				Required:    true,
				Sensitive:   true,
				Description: "The application's client secret. Write-only in the API: it is never read back, so a change made outside Terraform cannot be detected. Stored in the Terraform state.",
				Validators:  []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"email_claim": schema.StringAttribute{
				Computed:      true,
				Description:   "The ID token claim that holds the person's email address: `email`, `preferred_username` or `upn`. Read-only here: an owner sets it in the dashboard, because a token may not.",
				PlanModifiers: computed,
			},
			"trust_unverified_email": schema.BoolAttribute{
				Computed:      true,
				Description:   "Whether the provider's email address is accepted without an `email_verified` claim (Microsoft Entra needs it). Read-only here: an owner sets it in the dashboard, because a token may not.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"redirect_uri": schema.StringAttribute{Computed: true, Description: "The redirect URI to register with the identity provider.", PlanModifiers: computed},
			"discovery": schema.SingleNestedAttribute{
				Computed:    true,
				Description: "What the API read from the identity provider's discovery document when the connection was made. Read-only. It changes only when someone refreshes the connection in the dashboard or the API.",
				Attributes: map[string]schema.Attribute{
					"authorization_endpoint":     schema.StringAttribute{Computed: true, Description: "The provider's authorization endpoint."},
					"token_endpoint":             schema.StringAttribute{Computed: true, Description: "The provider's token endpoint."},
					"jwks_uri":                   schema.StringAttribute{Computed: true, Description: "The URL of the provider's signing keys."},
					"token_endpoint_auth_method": schema.StringAttribute{Computed: true, Description: "How the API authenticates to the token endpoint: `client_secret_basic` or `client_secret_post`."},
					"refreshed_at":               schema.StringAttribute{Computed: true, Description: "When the discovery document was last read (RFC 3339)."},
				},
			},
			"client_secret_updated_at": schema.StringAttribute{Computed: true, Description: "When the API last stored a client secret for this connection (RFC 3339). A value later than the last apply means the secret was changed outside Terraform.", PlanModifiers: []planmodifier.String{}},
		},
	}
}

func (r *ssoConnectionResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.c = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (m *ssoConnectionModel) set(c *client.SSOConnection) {
	m.ID = types.StringValue(c.ID)
	m.Name = types.StringValue(c.Name)
	m.Issuer = types.StringValue(c.Issuer)
	m.ClientID = types.StringValue(c.ClientID)
	m.EmailClaim = types.StringValue(c.EmailClaim)
	m.TrustUnverifiedEmail = types.BoolValue(c.TrustUnverifiedEmail)
	m.RedirectURI = types.StringValue(c.RedirectURI)
	m.ClientSecretUpdatedAt = types.StringPointerValue(c.ClientSecretUpdated)
	if c.Discovery != nil {
		m.Discovery = types.ObjectValueMust(discoveryTypes, map[string]attr.Value{
			"authorization_endpoint":     types.StringValue(c.Discovery.AuthorizationEndpoint),
			"token_endpoint":             types.StringValue(c.Discovery.TokenEndpoint),
			"jwks_uri":                   types.StringValue(c.Discovery.JWKSURI),
			"token_endpoint_auth_method": types.StringValue(c.Discovery.TokenEndpointAuthMethod),
			"refreshed_at":               types.StringValue(c.Discovery.RefreshedAt),
		})
	} else {
		m.Discovery = types.ObjectNull(discoveryTypes)
	}
}

func (r *ssoConnectionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ssoConnectionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	conn, err := r.c.CreateSSOConnection(ctx, client.SSOConnectionCreate{
		Name:         plan.Name.ValueString(),
		Issuer:       plan.Issuer.ValueString(),
		ClientID:     plan.ClientID.ValueString(),
		ClientSecret: plan.ClientSecret.ValueString(),
	})
	if err != nil {
		apiDiag(&resp.Diagnostics, "Could not create the SSO connection", err)
		return
	}
	plan.set(conn)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *ssoConnectionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ssoConnectionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	conn, err := r.c.GetSSOConnection(ctx, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiDiag(&resp.Diagnostics, "Could not read the SSO connection", err)
		return
	}
	state.set(conn) // client_secret stays as it is in the state: the API never returns it
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *ssoConnectionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state ssoConnectionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var up client.SSOConnectionUpdate
	if !plan.Name.Equal(state.Name) {
		up.Name = plan.Name.ValueStringPointer()
	}
	if !plan.ClientID.Equal(state.ClientID) {
		up.ClientID = plan.ClientID.ValueStringPointer()
	}
	if !plan.ClientSecret.Equal(state.ClientSecret) {
		up.ClientSecret = plan.ClientSecret.ValueStringPointer()
	}
	conn, err := r.c.UpdateSSOConnection(ctx, state.ID.ValueString(), up)
	if err != nil {
		apiDiag(&resp.Diagnostics, "Could not update the SSO connection", err)
		return
	}
	plan.set(conn)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *ssoConnectionResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state ssoConnectionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.c.DeleteSSOConnection(ctx, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		apiDiag(&resp.Diagnostics, "Could not delete the SSO connection", err)
	}
}

func (r *ssoConnectionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
