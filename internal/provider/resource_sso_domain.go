package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ailio-technologies-inc/terraform-provider-heytownhall/internal/client"
)

var (
	_ resource.Resource                = &ssoDomainResource{}
	_ resource.ResourceWithConfigure   = &ssoDomainResource{}
	_ resource.ResourceWithImportState = &ssoDomainResource{}
)

// NewSSODomainResource is the heytownhall_sso_domain resource.
func NewSSODomainResource() resource.Resource { return &ssoDomainResource{} }

type ssoDomainResource struct{ c *client.Client }

type ssoDomainModel struct {
	ID                 types.String `tfsdk:"id"`
	ConnectionID       types.String `tfsdk:"connection_id"`
	Domain             types.String `tfsdk:"domain"`
	DomainID           types.String `tfsdk:"domain_id"`
	VerificationRecord types.Object `tfsdk:"verification_record"`
	VerifiedAt         types.String `tfsdk:"verified_at"`
}

var verificationRecordTypes = map[string]attr.Type{"type": types.StringType, "name": types.StringType, "value": types.StringType}

func (r *ssoDomainResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_sso_domain"
}

func (r *ssoDomainResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "An email domain that a single sign-on connection covers. People with an address at a verified domain sign in through the connection.\n\n" +
			"Creating the resource claims the domain but does not verify it. Publish `verification_record` in the domain's DNS, then confirm it with `heytownhall_sso_domain_verification`. " +
			"`verification_record` is known as soon as the domain is created, so a DNS resource in the same configuration can use it (see the examples). " +
			"A verified domain belongs to one organisation. The API refuses free-mail domains such as `gmail.com`.\n\n" +
			"Import with `<connection id>/<domain id>`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "`<connection id>/<domain id>`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"connection_id": schema.StringAttribute{
				Required:      true,
				Description:   "The id of the `heytownhall_sso_connection`. Changing it replaces the domain.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"domain": schema.StringAttribute{
				Required:      true,
				Description:   "The email domain, in lowercase, such as `acme.com`. Write a non-ASCII domain as punycode. Changing it replaces the domain.",
				Validators:    []validator.String{domainValidator{}},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"domain_id": schema.StringAttribute{
				Computed:      true,
				Description:   "The domain's id within the connection.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"verification_record": schema.SingleNestedAttribute{
				Computed:    true,
				Description: "The DNS record that proves control of the domain. It stays the same for as long as the domain is claimed.",
				Attributes: map[string]schema.Attribute{
					"type":  schema.StringAttribute{Computed: true, Description: "The record type. Always `TXT`."},
					"name":  schema.StringAttribute{Computed: true, Description: "The full record name, such as `_heytownhall-verify.acme.com`."},
					"value": schema.StringAttribute{Computed: true, Description: "The record value, such as `heytownhall-verify=...`."},
				},
				PlanModifiers: []planmodifier.Object{objectplanmodifier.UseStateForUnknown()},
			},
			"verified_at": schema.StringAttribute{
				Computed:    true,
				Description: "When the API found the record (RFC 3339). Null until the domain is verified.",
			},
		},
	}
}

func (r *ssoDomainResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.c = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (m *ssoDomainModel) set(d *client.SSODomain) {
	m.ID = types.StringValue(d.ConnectionID + "/" + d.ID)
	m.ConnectionID = types.StringValue(d.ConnectionID)
	m.DomainID = types.StringValue(d.ID)
	m.Domain = types.StringValue(d.Domain)
	m.VerifiedAt = types.StringPointerValue(d.VerifiedAt)
	if d.Verification != nil {
		m.VerificationRecord = types.ObjectValueMust(verificationRecordTypes, map[string]attr.Value{
			"type":  types.StringValue(d.Verification.Type),
			"name":  types.StringValue(d.Verification.Name),
			"value": types.StringValue(d.Verification.Value),
		})
	} else if m.VerificationRecord.IsNull() || m.VerificationRecord.IsUnknown() {
		m.VerificationRecord = types.ObjectNull(verificationRecordTypes)
	}
}

func splitDomainID(id string) (conn, dom string, err error) {
	parts := strings.Split(id, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("%q is not of the form <connection id>/<domain id>", id)
	}
	return parts[0], parts[1], nil
}

func (r *ssoDomainResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ssoDomainModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	d, err := r.c.CreateSSODomain(ctx, plan.ConnectionID.ValueString(), plan.Domain.ValueString())
	if err != nil {
		apiDiag(&resp.Diagnostics, "Could not claim the domain", err)
		return
	}
	if d.ConnectionID == "" {
		d.ConnectionID = plan.ConnectionID.ValueString()
	}
	plan.set(d)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *ssoDomainResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ssoDomainModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	cid, did, err := splitDomainID(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid id", err.Error())
		return
	}
	d, err := r.c.GetSSODomain(ctx, cid, did)
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiDiag(&resp.Diagnostics, "Could not read the domain", err)
		return
	}
	if d.ConnectionID == "" {
		d.ConnectionID = cid
	}
	state.set(d)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

// Update never changes anything remotely: every argument forces a replacement. It keeps the state in step with the plan.
func (r *ssoDomainResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var state ssoDomainModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *ssoDomainResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state ssoDomainModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	cid, did, err := splitDomainID(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid id", err.Error())
		return
	}
	if err := r.c.DeleteSSODomain(ctx, cid, did); err != nil && !client.IsNotFound(err) {
		apiDiag(&resp.Diagnostics, "Could not remove the domain", err)
	}
}

func (r *ssoDomainResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if _, _, err := splitDomainID(req.ID); err != nil {
		resp.Diagnostics.AddError("Invalid import id", err.Error())
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
