package provider

import (
	"context"

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
	_ resource.Resource                = &memberResource{}
	_ resource.ResourceWithConfigure   = &memberResource{}
	_ resource.ResourceWithImportState = &memberResource{}
)

// NewMemberResource is the heytownhall_member resource.
func NewMemberResource() resource.Resource { return &memberResource{} }

type memberResource struct{ c *client.Client }

type memberModel struct {
	ID       types.String `tfsdk:"id"`
	UserID   types.String `tfsdk:"user_id"`
	Role     types.String `tfsdk:"role"`
	Email    types.String `tfsdk:"email"`
	Name     types.String `tfsdk:"name"`
	JoinedAt types.String `tfsdk:"joined_at"`
}

func (r *memberResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_member"
}

func (r *memberResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The role of a person who is already a member of the organisation.\n\n" +
			"This resource does not add people: a person joins by accepting an invitation (see `heytownhall_invitation`). Creating the resource adopts the member and sets the role. " +
			"Destroying it removes the person from the organisation.\n\n" +
			"A management token can never create, promote, demote or remove an owner. Owners are managed in the dashboard only. Pointing this resource at an owner fails with the API's `owner_protected` error.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "The member's user id. Same as `user_id`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"user_id": schema.StringAttribute{
				Required:      true,
				Description:   "The user id of an existing member. Look it up by email with the `heytownhall_members` data source. Changing it replaces the resource.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"role": schema.StringAttribute{
				Required:    true,
				Description: "The member's role. Only `member` can be set through the API.",
				Validators:  []validator.String{stringvalidator.OneOf(assignableRoles...)},
			},
			"email": schema.StringAttribute{
				Computed:      true,
				Description:   "The member's email address.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Computed:      true,
				Description:   "The member's name.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"joined_at": schema.StringAttribute{
				Computed:      true,
				Description:   "When the person joined (RFC 3339).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *memberResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.c = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (m *memberModel) set(x *client.Member) {
	m.ID = types.StringValue(x.UserID)
	m.UserID = types.StringValue(x.UserID)
	m.Role = types.StringValue(x.Role)
	m.Email = types.StringValue(x.Email)
	m.Name = types.StringValue(x.Name)
	m.JoinedAt = types.StringValue(x.JoinedAt)
}

func (r *memberResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan memberModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := plan.UserID.ValueString()
	if id == "" {
		resp.Diagnostics.AddAttributeError(path.Root("user_id"), "No user id yet",
			"user_id is empty. If it comes from heytownhall_invitation.accepted_user_id, the person has not accepted the invitation yet. Apply again after they accept.")
		return
	}
	got, err := r.c.GetMember(ctx, id)
	if client.IsNotFound(err) {
		resp.Diagnostics.AddAttributeError(path.Root("user_id"), "No such member",
			"There is no member with user id "+id+". A person becomes a member by accepting an invitation. Create a heytownhall_invitation first and add this resource once it is accepted.")
		return
	}
	if err != nil {
		apiDiag(&resp.Diagnostics, "Could not read the member", err)
		return
	}
	// Always send the role: for an owner the API refuses with owner_protected, and its message is what the person should read.
	if got.Role != plan.Role.ValueString() || got.Role == "owner" {
		got, err = r.c.UpdateMember(ctx, id, plan.Role.ValueString())
		if err != nil {
			apiDiag(&resp.Diagnostics, "Could not set the member's role", err)
			return
		}
	}
	plan.set(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *memberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state memberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := r.c.GetMember(ctx, state.UserID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiDiag(&resp.Diagnostics, "Could not read the member", err)
		return
	}
	state.set(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *memberResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state memberModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := r.c.UpdateMember(ctx, state.UserID.ValueString(), plan.Role.ValueString())
	if err != nil {
		apiDiag(&resp.Diagnostics, "Could not set the member's role", err)
		return
	}
	plan.set(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *memberResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state memberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.c.DeleteMember(ctx, state.UserID.ValueString()); err != nil && !client.IsNotFound(err) {
		apiDiag(&resp.Diagnostics, "Could not remove the member", err)
	}
}

func (r *memberResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("user_id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
