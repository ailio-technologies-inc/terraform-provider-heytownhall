package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ailio-technologies-inc/terraform-provider-heytownhall/internal/client"
)

var (
	_ resource.Resource                   = &ssoActivationResource{}
	_ resource.ResourceWithConfigure      = &ssoActivationResource{}
	_ resource.ResourceWithImportState    = &ssoActivationResource{}
	_ resource.ResourceWithValidateConfig = &ssoActivationResource{}
)

// NewSSOActivationResource is the heytownhall_sso_activation resource.
func NewSSOActivationResource() resource.Resource { return &ssoActivationResource{} }

type ssoActivationResource struct{ c *client.Client }

type ssoActivationModel struct {
	ID           types.String `tfsdk:"id"`
	ConnectionID types.String `tfsdk:"connection_id"`
	Enabled      types.Bool   `tfsdk:"enabled"`
	Enforced     types.Bool   `tfsdk:"enforced"`
}

func (r *ssoActivationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_sso_activation"
}

func (r *ssoActivationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Turns a single sign-on connection on, and optionally requires it.\n\n" +
			"This is a separate resource, and not two arguments of `heytownhall_sso_connection`, because the API only enables a connection that has a verified domain. " +
			"A verified domain needs the connection, so `enabled` on the connection itself would make a dependency loop. " +
			"Here the chain is a line: connection, domain, DNS record, verification, activation.\n\n" +
			"**Enforcing** sign-in (`enforced = true`) means that people with an address at one of the connection's verified domains can sign in only through the connection. " +
			"Password, email-link and social sign-ins are refused for them, the non-SSO sessions of the organisation's non-owner members at those domains end when you enforce, and they are told by email. An owner of the organisation can still sign in with a password; each such sign-in is recorded and every owner is told by email. " +
			"Enforced needs enabled.\n\n" +
			"**Scopes.** Turning a connection on needs both `sso:write` and `members:write` on the token, because people who sign in through single sign-on join the organisation as members.\n\n" +
			"**DNS is checked again.** Enabling and enforcing make the API look for the domain's TXT record again. If the record has been removed, the API answers `domain_not_verified`, and the domain reads as unverified. Leave the record published for as long as the connection is in use.\n\n" +
			"Destroying the resource disables the connection and stops enforcing it. Disabling a connection ends the sessions it opened.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Same as `connection_id`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"connection_id": schema.StringAttribute{
				Required:      true,
				Description:   "The id of the `heytownhall_sso_connection`. Changing it replaces the resource.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"enabled": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
				Description: "Whether the connection signs people in. Defaults to true. The connection needs at least one verified domain.",
			},
			"enforced": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
				Description: "Whether people at the connection's verified domains must use it. Defaults to false. Needs `enabled = true`.",
			},
		},
	}
}

func (r *ssoActivationResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var cfg ssoActivationModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// enabled defaults to true, so only an explicit false next to enforced = true is an error.
	if !cfg.Enforced.IsNull() && !cfg.Enforced.IsUnknown() && cfg.Enforced.ValueBool() &&
		!cfg.Enabled.IsNull() && !cfg.Enabled.IsUnknown() && !cfg.Enabled.ValueBool() {
		resp.Diagnostics.AddAttributeError(path.Root("enforced"), "Enforced needs enabled", "enforced = true requires enabled = true. A connection that is off cannot be required.")
	}
}

func (r *ssoActivationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.c = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *ssoActivationResource) apply(ctx context.Context, plan *ssoActivationModel) error {
	en, enf := plan.Enabled.ValueBool(), plan.Enforced.ValueBool()
	conn, err := r.c.UpdateSSOConnection(ctx, plan.ConnectionID.ValueString(), client.SSOConnectionUpdate{Enabled: &en, Enforced: &enf})
	if err != nil {
		return err
	}
	plan.ID = plan.ConnectionID
	plan.Enabled, plan.Enforced = types.BoolValue(conn.Enabled), types.BoolValue(conn.Enforced)
	return nil
}

func (r *ssoActivationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ssoActivationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.apply(ctx, &plan); err != nil {
		apiDiag(&resp.Diagnostics, "Could not turn the connection on", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *ssoActivationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ssoActivationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ConnectionID.ValueString()
	if id == "" {
		id = state.ID.ValueString()
	}
	conn, err := r.c.GetSSOConnection(ctx, id)
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiDiag(&resp.Diagnostics, "Could not read the SSO connection", err)
		return
	}
	state.ID, state.ConnectionID = types.StringValue(conn.ID), types.StringValue(conn.ID)
	state.Enabled, state.Enforced = types.BoolValue(conn.Enabled), types.BoolValue(conn.Enforced)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *ssoActivationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan ssoActivationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.apply(ctx, &plan); err != nil {
		apiDiag(&resp.Diagnostics, "Could not change the connection", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *ssoActivationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state ssoActivationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	off := false
	_, err := r.c.UpdateSSOConnection(ctx, state.ConnectionID.ValueString(), client.SSOConnectionUpdate{Enabled: &off, Enforced: &off})
	if err != nil && !client.IsNotFound(err) {
		apiDiag(&resp.Diagnostics, "Could not turn the connection off", err)
	}
}

func (r *ssoActivationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("connection_id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
