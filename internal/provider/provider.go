// Package provider is the Terraform provider for Hey Town Hall Data.
package provider

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/ailio-technologies-inc/terraform-provider-heytownhall/internal/client"
)

var _ provider.Provider = &heytownhallProvider{}

type heytownhallProvider struct{ version string }

// New returns a provider factory.
func New(version string) func() provider.Provider {
	return func() provider.Provider { return &heytownhallProvider{version: version} }
}

type providerModel struct {
	Token    types.String `tfsdk:"token"`
	Endpoint types.String `tfsdk:"endpoint"`
}

func (p *heytownhallProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "heytownhall"
	resp.Version = p.version
}

func (p *heytownhallProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a Hey Town Hall Data organisation: API keys, team members and invitations, the monthly spending cap, the organisation's name, and single sign-on connections.",
		Attributes: map[string]schema.Attribute{
			"token": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Description: "A management token (it starts with `htd_mgmt_`). An organisation owner creates it in the dashboard. " +
					"Can also be set with the `HEYTOWNHALL_MANAGEMENT_TOKEN` environment variable. A data API key (`htd_test_` or `htd_live_`) does not work here.",
			},
			"endpoint": schema.StringAttribute{
				Optional: true,
				Description: "The API's base URL. Defaults to `" + client.DefaultEndpoint + "`. " +
					"Can also be set with the `HEYTOWNHALL_ENDPOINT` environment variable. Only needed for testing against another deployment.",
			},
		},
	}
}

type tflogLogger struct{}

func (tflogLogger) Debug(ctx context.Context, msg string, fields map[string]any) {
	tflog.Debug(ctx, msg, fields)
}

func (p *heytownhallProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// A value that is not known yet (it comes from another resource): wait for apply.
	if cfg.Token.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("token"), "Unknown management token", "The token is not known until apply. Set it from a variable or the environment, not from another resource.")
	}
	if cfg.Endpoint.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("endpoint"), "Unknown endpoint", "The endpoint is not known until apply. Set it from a variable or the environment.")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	token := os.Getenv("HEYTOWNHALL_MANAGEMENT_TOKEN")
	if !cfg.Token.IsNull() {
		token = cfg.Token.ValueString()
	}
	endpoint := os.Getenv("HEYTOWNHALL_ENDPOINT")
	if !cfg.Endpoint.IsNull() {
		endpoint = cfg.Endpoint.ValueString()
	}
	if endpoint == "" {
		endpoint = client.DefaultEndpoint
	}

	if err := client.CheckToken(token); err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("token"), "Missing or wrong management token", err.Error())
		return
	}
	if u, err := url.Parse(endpoint); err != nil || u.Host == "" {
		resp.Diagnostics.AddAttributeError(path.Root("endpoint"), "Invalid endpoint", fmt.Sprintf("%q is not a URL.", endpoint))
		return
	} else if u.Scheme != "https" && !isLoopback(u.Hostname()) {
		resp.Diagnostics.AddAttributeError(path.Root("endpoint"), "Invalid endpoint", "The endpoint must use https. Plain http is accepted only for localhost.")
		return
	}

	c, err := client.New(client.Config{Endpoint: endpoint, Token: token, Version: p.version, Logger: tflogLogger{}})
	if err != nil {
		resp.Diagnostics.AddError("Could not set up the API client", err.Error())
		return
	}
	resp.DataSourceData = c
	resp.ResourceData = c
}

func isLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1" || strings.HasSuffix(host, ".localhost")
}

func (p *heytownhallProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewAPIKeyResource,
		NewMemberResource,
		NewInvitationResource,
		NewSpendCapResource,
		NewOrganizationResource,
		NewSSOConnectionResource,
		NewSSODomainResource,
		NewSSODomainVerificationResource,
		NewSSOActivationResource,
	}
}

func (p *heytownhallProvider) DataSources(context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewOrganizationDataSource,
		NewUsageDataSource,
		NewAPIKeysDataSource,
		NewMembersDataSource,
		NewManagementTokensDataSource,
		NewCoverageDataSource,
		NewTokenDataSource,
	}
}

// apiDiag turns an error from the client into a diagnostic that shows the code, message, parameter and request id.
func apiDiag(diags *diag.Diagnostics, summary string, err error) {
	diags.AddError(summary, err.Error())
}

// clientFrom reads the client the provider configured. It is nil while the provider is not configured yet.
func clientFrom(data any, diags *diag.Diagnostics) *client.Client {
	if data == nil {
		return nil
	}
	c, ok := data.(*client.Client)
	if !ok {
		diags.AddError("Unexpected provider data", fmt.Sprintf("Expected *client.Client, got %T. This is a bug in the provider.", data))
		return nil
	}
	return c
}
