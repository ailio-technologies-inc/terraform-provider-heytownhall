package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ailio-technologies-inc/terraform-provider-heytownhall/internal/client"
)

var (
	_ resource.Resource              = &ssoDomainVerificationResource{}
	_ resource.ResourceWithConfigure = &ssoDomainVerificationResource{}
)

// verifyPollInterval is how long the resource waits between checks of the DNS record. Tests shorten it.
var verifyPollInterval = 10 * time.Second

const defaultVerifyTimeout = 10 * time.Minute

// NewSSODomainVerificationResource is the heytownhall_sso_domain_verification resource.
func NewSSODomainVerificationResource() resource.Resource {
	return &ssoDomainVerificationResource{}
}

type ssoDomainVerificationResource struct{ c *client.Client }

type ssoDomainVerificationModel struct {
	ID          types.String   `tfsdk:"id"`
	SSODomainID types.String   `tfsdk:"sso_domain_id"`
	VerifiedAt  types.String   `tfsdk:"verified_at"`
	Timeouts    timeouts.Value `tfsdk:"timeouts"`
}

func (r *ssoDomainVerificationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_sso_domain_verification"
}

func (r *ssoDomainVerificationResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Waits until a domain's DNS record is found and the domain is verified. It makes no remote object of its own; it only asks the API to check the record, over and over, until the check passes or the timeout ends. " +
			"It works like `aws_acm_certificate_validation`.\n\n" +
			"Use it between the DNS record and whatever needs the verified domain, such as `heytownhall_sso_activation`. " +
			"If the domain is no longer verified when Terraform refreshes (the record was removed and the API checked again, or the domain was deleted), the resource leaves the state and the next apply verifies again.\n\n" +
			"Destroying the resource changes nothing remotely.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Same as `sso_domain_id`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"sso_domain_id": schema.StringAttribute{
				Required:      true,
				Description:   "The `id` of the `heytownhall_sso_domain` to verify. Changing it replaces the resource.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"verified_at": schema.StringAttribute{
				Computed:    true,
				Description: "When the API found the record (RFC 3339).",
			},
			"timeouts": timeouts.Attributes(ctx, timeouts.Opts{Create: true}),
		},
	}
}

func (r *ssoDomainVerificationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.c = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *ssoDomainVerificationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ssoDomainVerificationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := plan.Timeouts.Create(ctx, defaultVerifyTimeout)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	cid, did, err := splitDomainID(plan.SSODomainID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid sso_domain_id", err.Error())
		return
	}

	wait, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var last error
	for {
		d, err := r.c.VerifySSODomain(wait, cid, did)
		if err == nil && d.VerifiedAt != nil {
			plan.ID = plan.SSODomainID
			plan.VerifiedAt = types.StringValue(*d.VerifiedAt)
			resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
			return
		}
		switch {
		case err != nil && wait.Err() != nil && ctx.Err() == nil:
			// the timeout ended while a check was in flight
			if last == nil {
				last = err
			}
			resp.Diagnostics.AddError("Timed out waiting for the domain to verify", timeoutMessage(timeout, last))
			return
		case err == nil:
			last = fmt.Errorf("the API has not found the record yet")
		case client.HasCode(err, "domain_not_verified"):
			last = err // not there yet: keep waiting
		default:
			apiDiag(&resp.Diagnostics, "Could not verify the domain", err)
			return
		}
		select {
		case <-wait.Done():
			resp.Diagnostics.AddError("Timed out waiting for the domain to verify", timeoutMessage(timeout, last))
			return
		case <-time.After(verifyPollInterval):
		}
	}
}

func timeoutMessage(timeout time.Duration, last error) string {
	return fmt.Sprintf("The DNS record was not found within %s. Last answer: %s. Check that the TXT record from the domain's verification_record is published, and allow time for DNS to propagate. Apply again to wait again.", timeout, last)
}

func (r *ssoDomainVerificationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ssoDomainVerificationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	cid, did, err := splitDomainID(state.SSODomainID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid sso_domain_id", err.Error())
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
	if d.VerifiedAt == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	state.VerifiedAt = types.StringValue(*d.VerifiedAt)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

// Update runs only for a change to timeouts, which changes nothing remotely.
func (r *ssoDomainVerificationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state ssoDomainVerificationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ID, plan.VerifiedAt = state.ID, state.VerifiedAt
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *ssoDomainVerificationResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}
