package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ailio-technologies-inc/terraform-provider-heytownhall/internal/client"
)

var (
	_ resource.Resource                = &apiKeyResource{}
	_ resource.ResourceWithConfigure   = &apiKeyResource{}
	_ resource.ResourceWithImportState = &apiKeyResource{}
)

// NewAPIKeyResource is the heytownhall_api_key resource.
func NewAPIKeyResource() resource.Resource { return &apiKeyResource{} }

type apiKeyResource struct{ c *client.Client }

type apiKeyModel struct {
	ID        types.String `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	Kind      types.String `tfsdk:"kind"`
	Secret    types.String `tfsdk:"secret"`
	Display   types.String `tfsdk:"display"`
	CreatedAt types.String `tfsdk:"created_at"`
}

func (r *apiKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_api_key"
}

func (r *apiKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A data API key. Destroying the resource revokes the key.\n\n" +
			"The API shows a key's secret once, when the key is created. The provider keeps it in the Terraform state in `secret` (marked sensitive) " +
			"because nothing can read it again. Anyone who can read the state can read the secret, so keep the state in an encrypted backend with restricted access. " +
			"A key that is imported has no known secret, and `secret` stays null.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "The key's id.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "A name to tell the key apart, such as `production` or `staging`. Changing it renames the key.",
				Validators:  []validator.String{stringvalidator.LengthBetween(1, 80)},
			},
			"kind": schema.StringAttribute{
				Required:      true,
				Description:   "`sandbox` (fictional towns, free) or `live` (real data, billed). Changing it replaces the key: the old key is revoked and a new one with a new secret is made. A live key needs live billing to be open on the organisation.",
				Validators:    []validator.String{stringvalidator.OneOf(keyKinds...)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"secret": schema.StringAttribute{
				Computed:      true,
				Sensitive:     true,
				Description:   "The full key, for example `htd_live_...`. Known only when Terraform created the key; null for an imported key. Stored in the state.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"display": schema.StringAttribute{
				Computed:      true,
				Description:   "The key's prefix and last four characters, as the dashboard shows it.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_at": schema.StringAttribute{
				Computed:      true,
				Description:   "When the key was created (RFC 3339).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *apiKeyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.c = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *apiKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan apiKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	key, secret, err := r.c.CreateAPIKey(ctx, plan.Name.ValueString(), plan.Kind.ValueString())
	if err != nil {
		apiDiag(&resp.Diagnostics, "Could not create the API key", err)
		return
	}
	if secret == nil {
		// The create reached the API but its answer was lost, and the retry got the stored answer, which has no secret.
		// A key whose secret nobody holds is of no use and a risk to leave, so revoke it and ask for another try.
		if rerr := r.c.RevokeAPIKey(ctx, key.ID); rerr != nil {
			resp.Diagnostics.AddError("The API key was created but its secret was lost", fmt.Sprintf("Key %s exists and could not be revoked automatically (%s). Revoke it in the dashboard, then apply again.", key.ID, rerr))
			return
		}
		resp.Diagnostics.AddError("The API key's secret was lost", fmt.Sprintf("The API created key %s but its answer did not reach Terraform, and a repeated answer never contains the secret. The key was revoked. Apply again to create a new one.", key.ID))
		return
	}
	plan.ID = types.StringValue(key.ID)
	plan.Secret = types.StringValue(*secret)
	setKeyFields(&plan, key)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func setKeyFields(m *apiKeyModel, k *client.APIKey) {
	m.Name = types.StringValue(k.Name)
	m.Kind = types.StringValue(k.Kind)
	m.Display = types.StringValue(k.Display)
	m.CreatedAt = types.StringValue(k.CreatedAt)
}

func (r *apiKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state apiKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	key, err := r.c.GetAPIKey(ctx, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiDiag(&resp.Diagnostics, "Could not read the API key", err)
		return
	}
	if key.RevokedAt != nil {
		// Revoked outside Terraform: the key is gone, so the next plan makes a new one.
		resp.State.RemoveResource(ctx)
		return
	}
	setKeyFields(&state, key)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *apiKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state apiKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	key, err := r.c.UpdateAPIKey(ctx, state.ID.ValueString(), plan.Name.ValueString())
	if err != nil {
		apiDiag(&resp.Diagnostics, "Could not rename the API key", err)
		return
	}
	plan.ID = state.ID
	plan.Secret = state.Secret
	setKeyFields(&plan, key)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *apiKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state apiKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.c.RevokeAPIKey(ctx, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		apiDiag(&resp.Diagnostics, "Could not revoke the API key", err)
	}
}

func (r *apiKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.AddWarning("The secret of an imported key is unknown",
		"The API shows a key's secret only when the key is created, so `secret` stays null after an import. Read the secret from wherever it was stored when the key was made.")
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
