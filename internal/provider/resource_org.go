package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
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
	_ resource.Resource                = &organizationResource{}
	_ resource.ResourceWithConfigure   = &organizationResource{}
	_ resource.ResourceWithImportState = &organizationResource{}
	_ resource.Resource                = &spendCapResource{}
	_ resource.ResourceWithConfigure   = &spendCapResource{}
	_ resource.ResourceWithImportState = &spendCapResource{}
)

// NewOrganizationResource is the heytownhall_organization resource.
func NewOrganizationResource() resource.Resource { return &organizationResource{} }

// NewSpendCapResource is the heytownhall_spend_cap resource.
func NewSpendCapResource() resource.Resource { return &spendCapResource{} }

// ---- organization ----

type organizationResource struct{ c *client.Client }

type organizationModel struct {
	ID   types.String `tfsdk:"id"`
	Name types.String `tfsdk:"name"`
}

func (r *organizationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization"
}

func (r *organizationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The organisation's name. A token belongs to one organisation, so there is exactly one of these, and it already exists: creating the resource renames it.\n\n" +
			"Destroying the resource only removes it from the Terraform state. It does not delete the organisation and it does not change the name.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "The organisation's id.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "The organisation's name, as invoices and the dashboard show it.",
				Validators:  []validator.String{stringvalidator.LengthBetween(1, 120)},
			},
		},
	}
}

func (r *organizationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.c = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *organizationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan organizationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	org, err := r.c.UpdateOrganization(ctx, plan.Name.ValueString())
	if err != nil {
		apiDiag(&resp.Diagnostics, "Could not rename the organisation", err)
		return
	}
	plan.ID, plan.Name = types.StringValue(org.ID), types.StringValue(org.Name)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *organizationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state organizationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	org, err := r.c.GetOrganization(ctx)
	if err != nil {
		apiDiag(&resp.Diagnostics, "Could not read the organisation", err)
		return
	}
	state.ID, state.Name = types.StringValue(org.ID), types.StringValue(org.Name)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *organizationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan organizationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	org, err := r.c.UpdateOrganization(ctx, plan.Name.ValueString())
	if err != nil {
		apiDiag(&resp.Diagnostics, "Could not rename the organisation", err)
		return
	}
	plan.ID, plan.Name = types.StringValue(org.ID), types.StringValue(org.Name)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// Delete changes nothing remotely: the organisation is not Terraform's to delete.
func (r *organizationResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *organizationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// ---- spend cap ----

type spendCapResource struct{ c *client.Client }

type spendCapModel struct {
	ID    types.String `tfsdk:"id"`
	Cents types.Int64  `tfsdk:"cents"`
}

func (r *spendCapResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_spend_cap"
}

func (r *spendCapResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The organisation's monthly spending cap on live requests. There is one cap per organisation. " +
			"When the month's bill would pass it, live requests answer `402 spend_cap_reached`.\n\n" +
			"Destroying the resource removes the cap.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "The organisation's id.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"cents": schema.Int64Attribute{
				Optional:    true,
				Description: "The cap in US cents per month, at least 1. Leave it out (null) for no cap.",
				Validators:  []validator.Int64{int64validator.Between(1, 10000000000)},
			},
		},
	}
}

func (r *spendCapResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.c = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *spendCapResource) apply(ctx context.Context, plan *spendCapModel) error {
	got, err := r.c.PutSpendCap(ctx, plan.Cents.ValueInt64Pointer())
	if err != nil {
		return err
	}
	plan.ID = types.StringValue(got.OrganizationID)
	plan.Cents = types.Int64PointerValue(got.Cents)
	return nil
}

func (r *spendCapResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan spendCapModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.apply(ctx, &plan); err != nil {
		apiDiag(&resp.Diagnostics, "Could not set the spending cap", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *spendCapResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state spendCapModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := r.c.GetSpendCap(ctx)
	if err != nil {
		apiDiag(&resp.Diagnostics, "Could not read the spending cap", err)
		return
	}
	state.ID = types.StringValue(got.OrganizationID)
	state.Cents = types.Int64PointerValue(got.Cents)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *spendCapResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan spendCapModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.apply(ctx, &plan); err != nil {
		apiDiag(&resp.Diagnostics, "Could not set the spending cap", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *spendCapResource) Delete(ctx context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	if _, err := r.c.PutSpendCap(ctx, nil); err != nil {
		apiDiag(&resp.Diagnostics, "Could not remove the spending cap", err)
	}
}

func (r *spendCapResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
