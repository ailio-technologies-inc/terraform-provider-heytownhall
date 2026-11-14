package provider

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// Roles a token may give. Owners are managed in the dashboard only, so "owner" is not here.
var assignableRoles = []string{"member"}

var keyKinds = []string{"sandbox", "live"}

var domainLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// validateDomain checks the shape of an email domain: lowercase ASCII (punycode for others), two or more labels, no IP address.
func validateDomain(d string) error {
	if d != strings.ToLower(d) {
		return fmt.Errorf("must be lowercase")
	}
	if len(d) > 253 {
		return fmt.Errorf("must be at most 253 characters")
	}
	labels := strings.Split(d, ".")
	if len(labels) < 2 {
		return fmt.Errorf("must have at least two parts, such as example.com")
	}
	for _, l := range labels {
		if !domainLabel.MatchString(l) {
			return fmt.Errorf("%q is not a valid part of a domain name (use lowercase letters, digits and hyphens; write other scripts as punycode)", l)
		}
	}
	if regexp.MustCompile(`^[0-9]+$`).MatchString(labels[len(labels)-1]) {
		return fmt.Errorf("must not be an IP address")
	}
	return nil
}

type domainValidator struct{}

func (domainValidator) Description(context.Context) string {
	return "value must be a lowercase domain name such as example.com"
}
func (v domainValidator) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }
func (domainValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if err := validateDomain(req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid domain", fmt.Sprintf("%q: %s.", req.ConfigValue.ValueString(), err))
	}
}

type httpsURLValidator struct{}

func (httpsURLValidator) Description(context.Context) string {
	return "value must be an https URL with a host and no query or fragment"
}
func (v httpsURLValidator) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }
func (httpsURLValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	s := req.ConfigValue.ValueString()
	u, err := url.Parse(s)
	switch {
	case err != nil || u.Host == "":
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid issuer", fmt.Sprintf("%q is not a URL.", s))
	case u.Scheme != "https":
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid issuer", "The issuer must use https.")
	case u.RawQuery != "" || u.Fragment != "":
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid issuer", "The issuer must not have a query or a fragment.")
	}
}

type emailValidator struct{}

func (emailValidator) Description(context.Context) string { return "value must be an email address" }
func (v emailValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (emailValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	s := req.ConfigValue.ValueString()
	at := strings.LastIndex(s, "@")
	if at < 1 || at == len(s)-1 || !strings.Contains(s[at+1:], ".") || strings.ContainsAny(s, " \t\r\n,;<>") {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid email address", fmt.Sprintf("%q is not an email address.", s))
	}
}
