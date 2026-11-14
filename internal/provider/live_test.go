package provider

// Acceptance tests against a real management API. They run only when all of these are set:
//
//	HEYTOWNHALL_ENDPOINT              the API's base URL, for example http://127.0.0.1:8801
//	HEYTOWNHALL_MANAGEMENT_TOKEN      a management token with every scope (keys, members, settings, sso)
//	HEYTOWNHALL_TEST_ALLOW_WRITES=1   you accept that the tests rename the organisation, set and remove its spending
//	                                  cap, and create and revoke keys and invitations. Use a scratch organisation.
//
// Optional, for the single sign-on refusals (an identity provider the API can reach for discovery):
//
//	HEYTOWNHALL_TEST_SSO_ISSUER, HEYTOWNHALL_TEST_SSO_CLIENT_ID, HEYTOWNHALL_TEST_SSO_CLIENT_SECRET
//
// `make testlive` runs them. The provider reads the endpoint and token from the same two environment variables as in use.
// What cannot be done from outside (accepting an invitation, publishing DNS) is covered by the fake-API tests.

import (
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func liveEnv(t *testing.T) (endpoint, token string) {
	t.Helper()
	endpoint, token = os.Getenv("HEYTOWNHALL_ENDPOINT"), os.Getenv("HEYTOWNHALL_MANAGEMENT_TOKEN")
	if endpoint == "" || token == "" {
		t.Skip("HEYTOWNHALL_ENDPOINT and HEYTOWNHALL_MANAGEMENT_TOKEN are not set")
	}
	if os.Getenv("HEYTOWNHALL_TEST_ALLOW_WRITES") != "1" {
		t.Skip("HEYTOWNHALL_TEST_ALLOW_WRITES=1 is not set: these tests change the organisation")
	}
	return endpoint, token
}

// liveCall makes a request outside Terraform, as a person in the dashboard would.
func liveCall(t *testing.T, method, path string) {
	t.Helper()
	endpoint, token := liveEnv(t)
	req, _ := http.NewRequest(method, endpoint+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("%s %s: HTTP %d", method, path, resp.StatusCode)
	}
}

func TestLive_organizationSpendCapAndKeys(t *testing.T) {
	liveEnv(t)
	name := fmt.Sprintf("Terraform live test %d", time.Now().Unix())
	accTest(t, resource.TestCase{Steps: []resource.TestStep{
		{
			Config: fmt.Sprintf(`
resource "heytownhall_organization" "o" {
  name = %q
}
resource "heytownhall_spend_cap" "c" {
  cents = 25000
}
resource "heytownhall_api_key" "k" {
  name = "terraform live test"
  kind = "sandbox"
}
data "heytownhall_organization" "o" {
  depends_on = [heytownhall_spend_cap.c, heytownhall_organization.o]
}
data "heytownhall_token" "self" {}
data "heytownhall_management_tokens" "all" {}
data "heytownhall_usage" "now" {}
`, name),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("heytownhall_organization.o", "name", name),
				resource.TestCheckResourceAttr("heytownhall_spend_cap.c", "cents", "25000"),
				resource.TestMatchResourceAttr("heytownhall_api_key.k", "secret", rx(`^htd_test_`)),
				resource.TestCheckResourceAttr("data.heytownhall_organization.o", "spend_cap_cents", "25000"),
				resource.TestCheckResourceAttr("data.heytownhall_token.self", "status", "active"),
				resource.TestMatchResourceAttr("data.heytownhall_usage.now", "month", rx(`^20[0-9]{2}-[01][0-9]$`)),
			),
		},
		{ResourceName: "heytownhall_api_key.k", ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"secret"}},
		{ResourceName: "heytownhall_spend_cap.c", ImportState: true, ImportStateVerify: true},
		{ResourceName: "heytownhall_organization.o", ImportState: true, ImportStateVerify: true},
	}})
}

func TestLive_keyRevokedOutsideTerraform(t *testing.T) {
	liveEnv(t)
	var id string
	cfg := `
resource "heytownhall_api_key" "k" {
  name = "terraform live revoke test"
  kind = "sandbox"
}`
	accTest(t, resource.TestCase{Steps: []resource.TestStep{
		{Config: cfg, Check: fromState("heytownhall_api_key.k", "id", &id)},
		{PreConfig: func() { liveCall(t, "DELETE", "/v1/management/api-keys/"+id) }, Config: cfg, PlanOnly: true, ExpectNonEmptyPlan: true},
	}})
}

func TestLive_ownerIsProtected(t *testing.T) {
	liveEnv(t)
	accTest(t, resource.TestCase{Steps: []resource.TestStep{{
		Config: `
data "heytownhall_members" "all" {}

locals {
  owner = [for m in data.heytownhall_members.all.members : m.user_id if m.role == "owner"][0]
}

resource "heytownhall_member" "owner" {
  user_id = local.owner
  role    = "member"
}`,
		ExpectError: rx(`(?s)owner_protected.*request id`),
	}}})
}

func TestLive_invitationPendingImportAndCancelledOutside(t *testing.T) {
	liveEnv(t)
	var id string
	cfg := fmt.Sprintf(`
resource "heytownhall_invitation" "i" {
  email = "terraform-live-%d@example.com"
}`, time.Now().UnixNano())
	accTest(t, resource.TestCase{Steps: []resource.TestStep{
		{Config: cfg, Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("heytownhall_invitation.i", "status", "pending"),
			fromState("heytownhall_invitation.i", "id", &id),
		)},
		{ResourceName: "heytownhall_invitation.i", ImportState: true, ImportStateVerify: true},
		{PreConfig: func() { liveCall(t, "DELETE", "/v1/management/invitations/"+id) }, Config: cfg, PlanOnly: true, ExpectNonEmptyPlan: true},
	}})
}

func TestLive_coverage(t *testing.T) {
	liveEnv(t)
	accTest(t, resource.TestCase{Steps: []resource.TestStep{{
		Config: `data "heytownhall_coverage" "ca" { country = "CA" }`,
		Check:  resource.TestCheckResourceAttrSet("data.heytownhall_coverage.ca", "jurisdictions.#"),
	}}})
}

func TestLive_ssoRefusals(t *testing.T) {
	liveEnv(t)
	issuer, id, secret := os.Getenv("HEYTOWNHALL_TEST_SSO_ISSUER"), os.Getenv("HEYTOWNHALL_TEST_SSO_CLIENT_ID"), os.Getenv("HEYTOWNHALL_TEST_SSO_CLIENT_SECRET")
	if issuer == "" || id == "" || secret == "" {
		t.Skip("HEYTOWNHALL_TEST_SSO_ISSUER, _CLIENT_ID and _CLIENT_SECRET are not set")
	}
	conn := fmt.Sprintf(`
resource "heytownhall_sso_connection" "c" {
  name          = "terraform live test"
  issuer        = %q
  client_id     = %q
  client_secret = %q
}
`, issuer, id, secret)
	accTest(t, resource.TestCase{Steps: []resource.TestStep{
		{Config: conn + `
resource "heytownhall_sso_domain" "d" {
  connection_id = heytownhall_sso_connection.c.id
  domain        = "gmail.com"
}`, ExpectError: rx(`domain_refused`)},
		{Config: conn + `
resource "heytownhall_sso_domain" "d" {
  connection_id = heytownhall_sso_connection.c.id
  domain        = "terraform-live-test.example"
}
resource "heytownhall_sso_activation" "a" {
  connection_id = heytownhall_sso_connection.c.id
  depends_on    = [heytownhall_sso_domain.d]
}`, ExpectError: rx(`domain_not_verified`)},
	}})
}
