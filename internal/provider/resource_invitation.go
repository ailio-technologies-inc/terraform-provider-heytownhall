package provider

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ailio-technologies-inc/terraform-provider-heytownhall/internal/client"
)

var (
	_ resource.Resource                = &invitationResource{}
	_ resource.ResourceWithConfigure   = &invitationResource{}
	_ resource.ResourceWithImportState = &invitationResource{}
)

// NewInvitationResource is the heytownhall_invitation resource.
func NewInvitationResource() resource.Resource { return &invitationResource{} }

type invitationResource struct{ c *client.Client }

type invitationModel struct {
	ID        types.String `tfsdk:"id"`
	Email     types.String `tfsdk:"email"`
	Role      types.String `tfsdk:"role"`
	Status    types.String `tfsdk:"status"`
	ExpiresAt types.String `tfsdk:"expires_at"`
	// AcceptedUserID is the user id of the member, once the invitation is accepted.
	AcceptedUserID types.String `tfsdk:"accepted_user_id"`
}

func (r *invitationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_invitation"
}

func (r *invitationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "An invitation to join the organisation, sent by email.\n\n" +
			"An invitation changes state on its own: the person accepts it, or it expires. The resource handles each case so that no plan keeps showing a change:\n\n" +
			"* **Accepted.** The resource stays in the state with `status = \"accepted\"` and Terraform plans nothing. The person is now a member. To manage their role, add a `heytownhall_member` (see the example). Destroying an accepted invitation does not remove the member.\n" +
			"* **Expired, cancelled, or rejected.** The resource leaves the state, so the next plan creates a new invitation and sends a new email. Remove the resource from the configuration if you do not want that.\n" +
			"* **Pending.** Destroying the resource cancels the invitation.\n\n" +
			"An organisation can hold 100 pending invitations and send 50 a day. Past either limit the API refuses, and the error is shown at once, without a retry; the daily limit clears as invitations pass 24 hours old. Inviting someone who was removed from the organisation lets them back in.\n\n" +
			"Changing `email` or `role` replaces the invitation (the old one is cancelled, a new email is sent).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "The invitation's id.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"email": schema.StringAttribute{
				Required:      true,
				Description:   "The address to invite. Changing it replaces the invitation.",
				Validators:    []validator.String{emailValidator{}},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"role": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Default:       stringdefault.StaticString("member"),
				Description:   "The role the person gets. Defaults to `member`. Only `member` can be given through the API: owners are managed in the dashboard. Changing it replaces the invitation.",
				Validators:    []validator.String{stringvalidator.OneOf(assignableRoles...)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"status": schema.StringAttribute{
				Computed:      true,
				Description:   "`pending` or `accepted`. An invitation that expired, was cancelled or was rejected leaves the state.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"accepted_user_id": schema.StringAttribute{
				Computed:    true,
				Description: "The user id of the member who accepted the invitation. Null until it is accepted. Use it as `user_id` of a `heytownhall_member`.",
			},
			"expires_at": schema.StringAttribute{
				Computed:      true,
				Description:   "When the invitation expires (RFC 3339).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *invitationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.c = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (m *invitationModel) set(i *client.Invitation, status string) {
	m.ID = types.StringValue(i.ID)
	// The API may change the case of an address. Keep what the configuration wrote when it only differs in case.
	if m.Email.IsNull() || m.Email.IsUnknown() || !strings.EqualFold(m.Email.ValueString(), i.Email) {
		m.Email = types.StringValue(i.Email)
	}
	m.Role = types.StringValue(i.Role)
	m.Status = types.StringValue(status)
	m.AcceptedUserID = types.StringPointerValue(i.AcceptedUserID)
	m.ExpiresAt = types.StringValue(i.ExpiresAt)
}

func (r *invitationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan invitationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	inv, err := r.c.CreateInvitation(ctx, plan.Email.ValueString(), plan.Role.ValueString())
	if err != nil {
		apiDiag(&resp.Diagnostics, "Could not send the invitation", err)
		return
	}
	plan.set(inv, "pending")
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *invitationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state invitationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if state.Status.ValueString() == "accepted" {
		// Accepted is final. Nothing about it can change, so there is nothing to refresh.
		return
	}
	inv, err := r.c.GetInvitation(ctx, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiDiag(&resp.Diagnostics, "Could not read the invitation", err)
		return
	}
	switch inv.Status {
	case "pending", "accepted":
		state.set(inv, inv.Status)
	default:
		// expired, cancelled or rejected: the invitation is over, so the next plan invites again.
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

// Update only runs for fields that do not force a replacement, and there are none. It stores the plan so the framework is satisfied.
func (r *invitationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state invitationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ID, plan.Status, plan.ExpiresAt, plan.AcceptedUserID = state.ID, state.Status, state.ExpiresAt, state.AcceptedUserID
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *invitationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state invitationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if state.Status.ValueString() == "accepted" {
		return // nothing to cancel; the person stays a member
	}
	if err := r.c.CancelInvitation(ctx, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		apiDiag(&resp.Diagnostics, "Could not cancel the invitation", err)
	}
}

func (r *invitationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
