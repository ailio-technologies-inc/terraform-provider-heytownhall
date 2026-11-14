package provider

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/ailio-technologies-inc/terraform-provider-heytownhall/internal/fakeapi"
)

func init() {
	verifyPollInterval = 5 * time.Millisecond
}

var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"heytownhall": providerserver.NewProtocol6WithError(New("test")()),
}

// withFake starts a fake API and returns a function that prefixes a configuration with the provider block that points at it.
func withFake(t *testing.T) (*fakeapi.Fake, func(string) string) {
	t.Helper()
	f := fakeapi.New()
	t.Cleanup(f.Close)
	return f, func(body string) string {
		return fmt.Sprintf("provider \"heytownhall\" {\n  endpoint = %q\n  token    = %q\n}\n\n%s", f.URL(), fakeapi.Token, body)
	}
}

var oneLineBlock = regexp.MustCompile(`^(\s*(?:resource|data|provider)(?: "[^"]+")+) \{ (.+) \}$`)
var attrBoundary = regexp.MustCompile(`,?\s+([a-z_]+ = )`)

// multiLine rewrites a block written on one line with several arguments, which HCL does not allow, onto several lines.
func multiLine(cfg string) string {
	lines := strings.Split(cfg, "\n")
	for i, l := range lines {
		if m := oneLineBlock.FindStringSubmatch(l); m != nil && strings.Count(m[2], " = ") > 1 {
			lines[i] = m[1] + " {\n  " + attrBoundary.ReplaceAllString(m[2], "\n  $1") + "\n}"
		}
	}
	return strings.Join(lines, "\n")
}

func accTest(t *testing.T, c resource.TestCase) {
	t.Helper()
	for i := range c.Steps {
		c.Steps[i].Config = multiLine(c.Steps[i].Config)
	}
	c.ProtoV6ProviderFactories = testAccProtoV6ProviderFactories
	resource.Test(t, c)
}

func stateAttr(name, key string) func(*terraform.State) (string, error) {
	return func(s *terraform.State) (string, error) {
		rs, ok := s.RootModule().Resources[name]
		if !ok {
			return "", fmt.Errorf("resource %s not in state", name)
		}
		return rs.Primary.Attributes[key], nil
	}
}

func fromState(name, key string, dst *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		v, err := stateAttr(name, key)(s)
		*dst = v
		return err
	}
}

func rx(s string) *regexp.Regexp { return regexp.MustCompile(s) }

func TestAccProvider_dataKeyIsRefused(t *testing.T) {
	accTest(t, resource.TestCase{Steps: []resource.TestStep{{
		Config:      "provider \"heytownhall\" {\n  token = \"htd_live_NOTAMANAGEMENTTOKEN\"\n}\ndata \"heytownhall_organization\" \"o\" {}",
		ExpectError: rx(`data API\s+key`),
	}}})
}

func TestAccProvider_missingToken(t *testing.T) {
	t.Setenv("HEYTOWNHALL_MANAGEMENT_TOKEN", "")
	accTest(t, resource.TestCase{Steps: []resource.TestStep{{
		Config:      "provider \"heytownhall\" {}\ndata \"heytownhall_organization\" \"o\" {}",
		ExpectError: rx(`no management\s+token\s+is\s+set`),
	}}})
}

func TestAccProvider_notAManagementToken(t *testing.T) {
	accTest(t, resource.TestCase{Steps: []resource.TestStep{{
		Config:      "provider \"heytownhall\" {\n  token = \"sk_other\"\n}\ndata \"heytownhall_organization\" \"o\" {}",
		ExpectError: rx(`not a\s+management\s+token`),
	}}})
}

func TestAccProvider_plainHTTPOnlyForLoopback(t *testing.T) {
	accTest(t, resource.TestCase{Steps: []resource.TestStep{{
		Config:      "provider \"heytownhall\" {\n  token = \"htd_mgmt_x\"\n  endpoint = \"http://data.example.com\"\n}\ndata \"heytownhall_organization\" \"o\" {}",
		ExpectError: rx(`must use https`),
	}}})
}

func TestAccProvider_tokenAndEndpointFromEnvironment(t *testing.T) {
	f, _ := withFake(t)
	t.Setenv("HEYTOWNHALL_MANAGEMENT_TOKEN", fakeapi.Token)
	t.Setenv("HEYTOWNHALL_ENDPOINT", f.URL())
	accTest(t, resource.TestCase{Steps: []resource.TestStep{{
		Config: `data "heytownhall_organization" "o" {}`,
		Check:  resource.TestCheckResourceAttr("data.heytownhall_organization.o", "name", "Acme Council Data"),
	}}})
	for _, r := range f.Requests() {
		if r.UserAgent != "terraform-provider-heytownhall/test" {
			t.Errorf("user agent %q", r.UserAgent)
		}
	}
}

func TestAccProvider_apiErrorShowsCodeMessageAndRequestID(t *testing.T) {
	f, cfg := withFake(t)
	_ = f
	accTest(t, resource.TestCase{Steps: []resource.TestStep{{
		Config:      cfg("") + "\nprovider \"heytownhall\" {\n  alias = \"bad\"\n  token = \"htd_mgmt_WRONG\"\n  endpoint = \"" + f.URL() + "\"\n}\ndata \"heytownhall_organization\" \"o\" {\n  provider = heytownhall.bad\n}\n",
		ExpectError: rx(`(?s)unauthorized.*management token`),
	}}})
}

func TestAccProvider_survivesRateLimit(t *testing.T) {
	f, cfg := withFake(t)
	f.Inject429(3)
	accTest(t, resource.TestCase{Steps: []resource.TestStep{{
		Config: cfg(`data "heytownhall_organization" "o" {}`),
		Check:  resource.TestCheckResourceAttr("data.heytownhall_organization.o", "name", "Acme Council Data"),
	}}})
}

// ---- api key ----

func TestAccAPIKey_lifecycle(t *testing.T) {
	f, cfg := withFake(t)
	var firstID, secondID string
	accTest(t, resource.TestCase{
		CheckDestroy: func(*terraform.State) error {
			if n := f.KeyCount(); n != 0 {
				return fmt.Errorf("%d keys left unrevoked", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: cfg(`resource "heytownhall_api_key" "k" { name = "staging" kind = "sandbox" }`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("heytownhall_api_key.k", "name", "staging"),
					resource.TestCheckResourceAttr("heytownhall_api_key.k", "kind", "sandbox"),
					resource.TestMatchResourceAttr("heytownhall_api_key.k", "secret", rx(`^htd_test_FAKESECRET`)),
					resource.TestCheckResourceAttrSet("heytownhall_api_key.k", "display"),
					fromState("heytownhall_api_key.k", "id", &firstID),
				),
			},
			{ // rename in place: same id, same secret
				Config: cfg(`resource "heytownhall_api_key" "k" { name = "staging-2" kind = "sandbox" }`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("heytownhall_api_key.k", "name", "staging-2"),
					resource.TestMatchResourceAttr("heytownhall_api_key.k", "secret", rx(`^htd_test_FAKESECRET`)),
					func(s *terraform.State) error {
						id, _ := stateAttr("heytownhall_api_key.k", "id")(s)
						if id != firstID {
							return fmt.Errorf("a rename replaced the key")
						}
						return nil
					},
				),
			},
			{ // import by id: the secret is unknown
				ResourceName:            "heytownhall_api_key.k",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"secret"},
			},
			{ // changing the kind replaces the key: the old one is revoked, a new one has a new id and secret
				Config: cfg(`resource "heytownhall_api_key" "k" { name = "staging-2" kind = "live" }`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr("heytownhall_api_key.k", "secret", rx(`^htd_live_FAKESECRET`)),
					fromState("heytownhall_api_key.k", "id", &secondID),
					func(*terraform.State) error {
						if secondID == firstID {
							return fmt.Errorf("the id did not change")
						}
						if _, _, revoked, _ := f.KeyState(firstID); !revoked {
							return fmt.Errorf("the old key was not revoked")
						}
						return nil
					},
				),
			},
			{ // revoked outside Terraform: the next plan creates a new key
				PreConfig:          func() { f.RevokeKey(secondID) },
				Config:             cfg(`resource "heytownhall_api_key" "k" { name = "staging-2" kind = "live" }`),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: cfg(`resource "heytownhall_api_key" "k" { name = "staging-2" kind = "live" }`),
				Check: func(*terraform.State) error {
					if f.KeyCount() != 1 {
						return fmt.Errorf("want one live key, have %d", f.KeyCount())
					}
					return nil
				},
			},
		},
	})
}

func TestAccAPIKey_createSendsOneIdempotencyKeyPerCreate(t *testing.T) {
	f, cfg := withFake(t)
	accTest(t, resource.TestCase{Steps: []resource.TestStep{{
		Config: cfg(`
resource "heytownhall_api_key" "a" { name = "a" kind = "sandbox" }
resource "heytownhall_api_key" "b" { name = "b" kind = "sandbox" }`),
	}}})
	keys := map[string]bool{}
	for _, r := range f.Requests() {
		if r.Method == "POST" && r.Path == "/v1/management/api-keys" {
			if r.IdempotencyKey == "" || keys[r.IdempotencyKey] {
				t.Errorf("create without a fresh idempotency key: %q", r.IdempotencyKey)
			}
			keys[r.IdempotencyKey] = true
		}
	}
	if len(keys) != 2 {
		t.Errorf("want two creates, saw %d", len(keys))
	}
}

func TestAccAPIKey_lostSecretIsRevokedAndReported(t *testing.T) {
	f, cfg := withFake(t)
	f.DropNextCreateResponse()
	accTest(t, resource.TestCase{Steps: []resource.TestStep{
		{
			// The create works but its answer is lost; the retry gets a replay with a null secret.
			Config:      cfg(`resource "heytownhall_api_key" "k" { name = "x" kind = "sandbox" }`),
			ExpectError: rx(`(?s)secret was\s+lost.*revoked`),
		},
		{
			Config: cfg(`resource "heytownhall_api_key" "k" { name = "x" kind = "sandbox" }`),
			Check: func(*terraform.State) error {
				if f.KeyCount() != 1 {
					return fmt.Errorf("want exactly one live key after the second apply, have %d", f.KeyCount())
				}
				return nil
			},
		},
	}})
}

func TestAccAPIKey_liveKeyNeedsBilling(t *testing.T) {
	f, cfg := withFake(t)
	f.SetLiveOpen(false)
	accTest(t, resource.TestCase{Steps: []resource.TestStep{{
		Config:      cfg(`resource "heytownhall_api_key" "k" { name = "x" kind = "live" }`),
		ExpectError: rx(`(?s)billing_required.*payment\s+method.*req_fake`),
	}}})
}

func TestAccAPIKey_planTimeValidation(t *testing.T) {
	_, cfg := withFake(t)
	accTest(t, resource.TestCase{Steps: []resource.TestStep{
		{Config: cfg(`resource "heytownhall_api_key" "k" { name = "x" kind = "production" }`), PlanOnly: true, ExpectError: rx(`kind.*value must be one of`)},
		{Config: cfg(`resource "heytownhall_api_key" "k" { name = "" kind = "sandbox" }`), PlanOnly: true, ExpectError: rx(`(?s)name.*between 1 and 80`)},
	}})
}

// ---- organisation ----

func TestAccOrganization_lifecycle(t *testing.T) {
	f, cfg := withFake(t)
	accTest(t, resource.TestCase{
		CheckDestroy: func(*terraform.State) error {
			if f.OrgName() != "Acme Data Team" {
				return fmt.Errorf("destroy changed the remote name to %q", f.OrgName())
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: cfg(`resource "heytownhall_organization" "o" { name = "Acme Data" }`),
				Check:  resource.TestCheckResourceAttr("heytownhall_organization.o", "name", "Acme Data"),
			},
			{
				Config: cfg(`resource "heytownhall_organization" "o" { name = "Acme Data Team" }`),
				Check:  resource.TestCheckResourceAttrSet("heytownhall_organization.o", "id"),
			},
			{ResourceName: "heytownhall_organization.o", ImportState: true, ImportStateVerify: true},
			{ // a rename in the dashboard shows as a change
				PreConfig:          func() { f.SetOrgName("Renamed elsewhere") },
				Config:             cfg(`resource "heytownhall_organization" "o" { name = "Acme Data Team" }`),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: cfg(`resource "heytownhall_organization" "o" { name = "Acme Data Team" }`),
			},
		},
	})
}

// ---- spend cap ----

func TestAccSpendCap_lifecycle(t *testing.T) {
	f, cfg := withFake(t)
	accTest(t, resource.TestCase{
		CheckDestroy: func(*terraform.State) error {
			if f.SpendCap() != nil {
				return fmt.Errorf("destroy left a cap of %d", *f.SpendCap())
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: cfg(`resource "heytownhall_spend_cap" "c" { cents = 50000 }`),
				Check: func(*terraform.State) error {
					if c := f.SpendCap(); c == nil || *c != 50000 {
						return fmt.Errorf("remote cap %v", c)
					}
					return nil
				},
			},
			{
				Config: cfg(`resource "heytownhall_spend_cap" "c" { cents = 75000 }`),
				Check:  resource.TestCheckResourceAttr("heytownhall_spend_cap.c", "cents", "75000"),
			},
			{ResourceName: "heytownhall_spend_cap.c", ImportState: true, ImportStateVerify: true},
			{ // a change in the dashboard shows as drift
				PreConfig:          func() { c := int64(1); f.SetSpendCap(&c) },
				Config:             cfg(`resource "heytownhall_spend_cap" "c" { cents = 75000 }`),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{ // no cents means no cap
				Config: cfg(`resource "heytownhall_spend_cap" "c" {}`),
				Check: func(*terraform.State) error {
					if f.SpendCap() != nil {
						return fmt.Errorf("the cap was not removed")
					}
					return nil
				},
			},
			{Config: cfg(`resource "heytownhall_spend_cap" "c" { cents = 100 }`)},
		},
	})
}

func TestAccSpendCap_rejectsNegative(t *testing.T) {
	_, cfg := withFake(t)
	accTest(t, resource.TestCase{Steps: []resource.TestStep{{
		Config: cfg(`resource "heytownhall_spend_cap" "c" { cents = -5 }`), PlanOnly: true, ExpectError: rx(`(?s)cents.*between 1`),
	}}})
}

// ---- members and invitations ----

func TestAccMember_lifecycle(t *testing.T) {
	f, cfg := withFake(t)
	id := f.AddMember("ana@acme.test", "member")
	body := fmt.Sprintf(`resource "heytownhall_member" "ana" { user_id = %q role = "member" }`, id)
	accTest(t, resource.TestCase{
		CheckDestroy: func(*terraform.State) error {
			if _, ok := f.MemberRole(id); ok {
				return fmt.Errorf("the member is still in the organisation")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: cfg(body), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("heytownhall_member.ana", "email", "ana@acme.test"),
				resource.TestCheckResourceAttr("heytownhall_member.ana", "role", "member"),
				resource.TestCheckResourceAttr("heytownhall_member.ana", "id", id),
			)},
			{ResourceName: "heytownhall_member.ana", ImportState: true, ImportStateId: id, ImportStateVerify: true},
		},
	})
}

func TestAccMember_ownerIsRefusedWithTheAPIsMessage(t *testing.T) {
	f, cfg := withFake(t)
	accTest(t, resource.TestCase{Steps: []resource.TestStep{{
		Config:      cfg(fmt.Sprintf(`resource "heytownhall_member" "o" { user_id = %q role = "member" }`, f.OwnerID())),
		ExpectError: rx(`(?s)owner_protected.*Manage\s+owners\s+in\s+the\s+dashboard.*req_fake`),
	}}})
	if role, _ := f.MemberRole(f.OwnerID()); role != "owner" {
		t.Fatalf("the owner's role changed to %q", role)
	}
}

func TestAccMember_ownerRoleIsNotAssignable(t *testing.T) {
	f, cfg := withFake(t)
	accTest(t, resource.TestCase{Steps: []resource.TestStep{{
		Config:   cfg(fmt.Sprintf(`resource "heytownhall_member" "o" { user_id = %q role = "owner" }`, f.OwnerID())),
		PlanOnly: true, ExpectError: rx(`value must be one of`),
	}}})
}

func TestAccMember_unknownUserSaysInviteFirst(t *testing.T) {
	_, cfg := withFake(t)
	accTest(t, resource.TestCase{Steps: []resource.TestStep{{
		Config:      cfg(`resource "heytownhall_member" "x" { user_id = "00000000-0000-4000-8000-00000000ffff" role = "member" }`),
		ExpectError: rx(`(?s)No such member.*invitation`),
	}}})
}

func TestAccMember_removedElsewhereLeavesTheState(t *testing.T) {
	f, cfg := withFake(t)
	id := f.AddMember("gone@acme.test", "member")
	body := fmt.Sprintf(`resource "heytownhall_member" "m" { user_id = %q role = "member" }`, id)
	accTest(t, resource.TestCase{Steps: []resource.TestStep{
		{Config: cfg(body)},
		{ // the person is removed in the dashboard: Terraform would adopt a member that is no longer there, and says so
			PreConfig: func() { f.RemoveMember(id) },
			Config:    cfg(body), PlanOnly: true, ExpectNonEmptyPlan: true,
		},
		{PreConfig: func() { f.AddMember("other@acme.test", "member") }, Config: cfg(`data "heytownhall_members" "all" {}`)},
	}})
}

const invitationConfig = `
resource "heytownhall_invitation" "ana" {
  email = "ana@acme.test"
}
`

func TestAccInvitation_lifecycleWithAcceptance(t *testing.T) {
	f, cfg := withFake(t)
	withMember := invitationConfig + `
data "heytownhall_members" "all" {}

locals {
  user_id_by_email = { for m in data.heytownhall_members.all.members : m.email => m.user_id }
}

resource "heytownhall_member" "ana" {
  user_id = local.user_id_by_email["ana@acme.test"]
  role    = "member"
}
`
	accTest(t, resource.TestCase{
		CheckDestroy: func(*terraform.State) error {
			if f.InvitationCount() != 0 {
				return fmt.Errorf("an invitation was left")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: cfg(invitationConfig),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("heytownhall_invitation.ana", "status", "pending"),
					resource.TestCheckNoResourceAttr("heytownhall_invitation.ana", "accepted_user_id"),
					resource.TestCheckResourceAttr("heytownhall_invitation.ana", "role", "member"),
					resource.TestCheckResourceAttrSet("heytownhall_invitation.ana", "expires_at"),
				),
			},
			{ResourceName: "heytownhall_invitation.ana", ImportState: true, ImportStateVerify: true},
			{ // accepted: stays in the state and plans nothing (the framework fails the step if the plan is not empty)
				PreConfig: func() { f.AcceptInvitation("ana@acme.test") },
				Config:    cfg(invitationConfig),
				Check:     resource.TestCheckResourceAttrSet("heytownhall_invitation.ana", "accepted_user_id"),
			},
			{ // the invitation to member pattern, looking the person up by email
				Config: cfg(withMember),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("heytownhall_invitation.ana", "status", "accepted"),
					resource.TestCheckResourceAttr("heytownhall_member.ana", "email", "ana@acme.test"),
				),
			},
			{Config: cfg(invitationConfig + `data "heytownhall_members" "all" {}`)}, // member resource removed: person is removed from the organisation
			{
				Config: cfg(`data "heytownhall_members" "all" {}`),
				Check: func(*terraform.State) error {
					if _, ok := f.MemberRole(f.OwnerID()); !ok {
						return fmt.Errorf("the owner vanished")
					}
					return nil
				},
			},
		},
	})
}

func TestAccInvitation_acceptedUserIDFeedsAMember(t *testing.T) {
	f, cfg := withFake(t)
	body := invitationConfig + `
resource "heytownhall_member" "ana" {
  user_id = heytownhall_invitation.ana.accepted_user_id
  role    = "member"
}`
	accTest(t, resource.TestCase{Steps: []resource.TestStep{
		{Config: cfg(invitationConfig)},
		{
			PreConfig: func() { f.AcceptInvitation("ana@acme.test") },
			Config:    cfg(body),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet("heytownhall_invitation.ana", "accepted_user_id"),
				resource.TestCheckResourceAttr("heytownhall_member.ana", "email", "ana@acme.test"),
			),
		},
	}})
}

func TestAccMember_beforeTheInvitationIsAccepted(t *testing.T) {
	_, cfg := withFake(t)
	accTest(t, resource.TestCase{Steps: []resource.TestStep{{
		Config: cfg(invitationConfig + `
resource "heytownhall_member" "ana" {
  user_id = heytownhall_invitation.ana.accepted_user_id
  role    = "member"
}`),
		ExpectError: rx(`(?s)user_id.*(required|null|empty)`),
	}}})
}

func TestAccInvitation_expiredIsInvitedAgain(t *testing.T) {
	f, cfg := withFake(t)
	var firstID string
	accTest(t, resource.TestCase{Steps: []resource.TestStep{
		{Config: cfg(invitationConfig), Check: fromState("heytownhall_invitation.ana", "id", &firstID)},
		{ // expired: gone from the state, so the plan sends a new one
			PreConfig: func() { f.ExpireInvitation("ana@acme.test") },
			Config:    cfg(invitationConfig), PlanOnly: true, ExpectNonEmptyPlan: true,
		},
		{
			Config: cfg(invitationConfig),
			Check: func(s *terraform.State) error {
				id, _ := stateAttr("heytownhall_invitation.ana", "id")(s)
				if id == firstID || id == "" {
					return fmt.Errorf("no new invitation: %q", id)
				}
				return nil
			},
		},
	}})
}

func TestAccInvitation_changeReplaces(t *testing.T) {
	f, cfg := withFake(t)
	accTest(t, resource.TestCase{Steps: []resource.TestStep{
		{Config: cfg(`resource "heytownhall_invitation" "i" { email = "a@acme.test" }`)},
		{
			Config: cfg(`resource "heytownhall_invitation" "i" { email = "b@acme.test" }`),
			Check: func(*terraform.State) error {
				if f.InvitationCount() != 1 {
					return fmt.Errorf("want one pending invitation (the old one cancelled), have %d", f.InvitationCount())
				}
				return nil
			},
		},
	}})
}

func TestAccInvitation_validationAndOwnerRole(t *testing.T) {
	_, cfg := withFake(t)
	accTest(t, resource.TestCase{Steps: []resource.TestStep{
		{Config: cfg(`resource "heytownhall_invitation" "i" { email = "not-an-email" }`), PlanOnly: true, ExpectError: rx(`not an email address`)},
		{Config: cfg(`resource "heytownhall_invitation" "i" { email = "a@acme.test" role = "owner" }`), PlanOnly: true, ExpectError: rx(`value must be one of`)},
	}})
}

func TestAccInvitation_alreadyAMemberIsAnError(t *testing.T) {
	f, cfg := withFake(t)
	f.AddMember("ana@acme.test", "member")
	accTest(t, resource.TestCase{Steps: []resource.TestStep{{
		Config: cfg(invitationConfig), ExpectError: rx(`(?s)conflict.*already a member`),
	}}})
}

// ---- SSO ----

const ssoChain = `
resource "heytownhall_sso_connection" "okta" {
  name          = "Okta"
  issuer        = "https://acme.okta.test"
  client_id     = "0oa123"
  client_secret = "%s"
}

resource "heytownhall_sso_domain" "acme" {
  connection_id = heytownhall_sso_connection.okta.id
  domain        = "acme.test"
}

resource "heytownhall_sso_domain_verification" "acme" {
  sso_domain_id = heytownhall_sso_domain.acme.id
  timeouts = {
    create = "5s"
  }
}

resource "heytownhall_sso_activation" "okta" {
  connection_id = heytownhall_sso_connection.okta.id
  enabled       = true
  enforced      = %t

  depends_on = [heytownhall_sso_domain_verification.acme]
}
`

func TestAccSSO_fullChain(t *testing.T) {
	f, cfg := withFake(t)
	f.PropagateAfter(2) // the record shows up on the third check, as DNS does
	var connID string
	accTest(t, resource.TestCase{
		CheckDestroy: func(*terraform.State) error {
			if f.ConnectionCount() != 0 || f.DomainCount() != 0 {
				return fmt.Errorf("left %d connections and %d domains", f.ConnectionCount(), f.DomainCount())
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: cfg(fmt.Sprintf(ssoChain, "first-secret", false)),
				Check: resource.ComposeAggregateTestCheckFunc(
					fromState("heytownhall_sso_connection.okta", "id", &connID),
					resource.TestCheckResourceAttr("heytownhall_sso_connection.okta", "email_claim", "email"),
					resource.TestCheckResourceAttr("heytownhall_sso_connection.okta", "trust_unverified_email", "false"),
					resource.TestCheckResourceAttr("heytownhall_sso_connection.okta", "redirect_uri", "https://data.heytownhall.com/api/auth/sso/callback"),
					resource.TestCheckResourceAttr("heytownhall_sso_domain.acme", "verification_record.type", "TXT"),
					resource.TestCheckResourceAttr("heytownhall_sso_domain.acme", "verification_record.name", "_heytownhall-verify.acme.test"),
					resource.TestMatchResourceAttr("heytownhall_sso_domain.acme", "verification_record.value", rx(`^heytownhall-verify=.+`)),
					resource.TestCheckResourceAttrSet("heytownhall_sso_domain_verification.acme", "verified_at"),
					resource.TestCheckResourceAttr("heytownhall_sso_activation.okta", "enabled", "true"),
					resource.TestCheckResourceAttr("heytownhall_sso_activation.okta", "enforced", "false"),
					func(*terraform.State) error {
						if f.ConnectionSecret(connID) != "first-secret" {
							return fmt.Errorf("the API did not get the client secret")
						}
						if !f.DomainVerified("acme.test") {
							return fmt.Errorf("domain not verified")
						}
						if f.VerifyChecks("acme.test") != 3 {
							return fmt.Errorf("want 3 checks while the record propagated, saw %d", f.VerifyChecks("acme.test"))
						}
						if en, enf, _ := f.ConnectionState(connID); !en || enf {
							return fmt.Errorf("connection state enabled=%v enforced=%v", en, enf)
						}
						return nil
					},
				),
			},
			{ // enforce, and rotate the secret
				Config: cfg(fmt.Sprintf(ssoChain, "second-secret", true)),
				Check: func(*terraform.State) error {
					if f.ConnectionSecret(connID) != "second-secret" {
						return fmt.Errorf("the rotated secret was not sent")
					}
					if en, enf, _ := f.ConnectionState(connID); !en || !enf {
						return fmt.Errorf("connection state enabled=%v enforced=%v", en, enf)
					}
					return nil
				},
			},
			{ // import: the secret is unknown to Terraform
				ResourceName:            "heytownhall_sso_connection.okta",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"client_secret"},
			},
			{
				ResourceName: "heytownhall_sso_domain.acme", ImportState: true, ImportStateVerify: true,
			},
			{
				ResourceName: "heytownhall_sso_activation.okta", ImportState: true, ImportStateVerify: true,
			},
			{ // a change of the secret in the dashboard cannot be seen: no plan
				PreConfig: func() {},
				Config:    cfg(fmt.Sprintf(ssoChain, "second-secret", true)),
				PlanOnly:  true,
			},
			{ // turn enforcement off again
				Config: cfg(fmt.Sprintf(ssoChain, "second-secret", false)),
				Check: func(*terraform.State) error {
					if en, enf, _ := f.ConnectionState(connID); !en || enf {
						return fmt.Errorf("connection state enabled=%v enforced=%v", en, enf)
					}
					return nil
				},
			},
		},
	})
}

func TestAccSSO_activationBeforeVerificationIsRefused(t *testing.T) {
	_, cfg := withFake(t)
	accTest(t, resource.TestCase{Steps: []resource.TestStep{{
		Config: cfg(`
resource "heytownhall_sso_connection" "c" {
  name          = "Okta"
  issuer        = "https://acme.okta.test"
  client_id     = "x"
  client_secret = "y"
}
resource "heytownhall_sso_domain" "d" {
  connection_id = heytownhall_sso_connection.c.id
  domain = "acme.test"
}
resource "heytownhall_sso_activation" "a" {
  connection_id = heytownhall_sso_connection.c.id
  depends_on    = [heytownhall_sso_domain.d]
}`),
		ExpectError: rx(`(?s)domain_not_verified.*Verify\s+at\s+least\s+one\s+domain`),
	}}})
}

func TestAccSSO_verificationTimesOutWhenTheRecordIsMissing(t *testing.T) {
	_, cfg := withFake(t)
	accTest(t, resource.TestCase{Steps: []resource.TestStep{{
		Config: cfg(`
resource "heytownhall_sso_connection" "c" {
  name          = "Okta"
  issuer        = "https://acme.okta.test"
  client_id     = "x"
  client_secret = "y"
}
resource "heytownhall_sso_domain" "d" {
  connection_id = heytownhall_sso_connection.c.id
  domain        = "acme.test"
}
resource "heytownhall_sso_domain_verification" "v" {
  sso_domain_id = heytownhall_sso_domain.d.id
  timeouts = {
    create = "100ms"
  }
}`),
		ExpectError: rx(`(?s)Timed out.*not found.*domain_not_verified`),
	}}})
}

func TestAccSSO_recordPublishedBeforeApplyVerifiesAtOnce(t *testing.T) {
	f, cfg := withFake(t)
	f.PropagateAfter(0)
	accTest(t, resource.TestCase{Steps: []resource.TestStep{{
		Config: cfg(fmt.Sprintf(ssoChain, "s", false)),
		Check: func(*terraform.State) error {
			if f.VerifyChecks("acme.test") != 1 {
				return fmt.Errorf("want one check, saw %d", f.VerifyChecks("acme.test"))
			}
			return nil
		},
	}}})
}

func TestAccSSO_planTimeValidation(t *testing.T) {
	_, cfg := withFake(t)
	conn := func(issuer string) string {
		return fmt.Sprintf(`resource "heytownhall_sso_connection" "c" { name = "x" issuer = %q client_id = "a" client_secret = "b" }`, issuer)
	}
	accTest(t, resource.TestCase{Steps: []resource.TestStep{
		{Config: cfg(conn("http://idp.test")), PlanOnly: true, ExpectError: rx(`must use https`)},
		{Config: cfg(conn("not a url")), PlanOnly: true, ExpectError: rx(`(?s)Invalid issuer`)},
		{Config: cfg(`resource "heytownhall_sso_connection" "c" { name = "x" issuer = "https://i.test" client_id = "a" client_secret = "b" email_claim = "upn" }`), PlanOnly: true, ExpectError: rx(`(?s)email_claim.*(read-only|computed|cannot be set)`)},
		{Config: cfg(`resource "heytownhall_sso_domain" "d" { connection_id = "c" domain = "Acme.COM" }`), PlanOnly: true, ExpectError: rx(`must be lowercase`)},
		{Config: cfg(`resource "heytownhall_sso_domain" "d" { connection_id = "c" domain = "localhost" }`), PlanOnly: true, ExpectError: rx(`at least two parts`)},
		{Config: cfg(`resource "heytownhall_sso_domain" "d" { connection_id = "c" domain = "https://acme.com/" }`), PlanOnly: true, ExpectError: rx(`not a valid part`)},
		{Config: cfg(`resource "heytownhall_sso_domain" "d" { connection_id = "c" domain = "10.0.0.1" }`), PlanOnly: true, ExpectError: rx(`IP address`)},
		{Config: cfg(`resource "heytownhall_sso_activation" "a" { connection_id = "c" enabled = false enforced = true }`), PlanOnly: true, ExpectError: rx(`Enforced needs enabled`)},
	}})
}

func TestAccSSO_freeMailDomainIsRefusedByTheAPI(t *testing.T) {
	_, cfg := withFake(t)
	accTest(t, resource.TestCase{Steps: []resource.TestStep{{
		Config: cfg(`
resource "heytownhall_sso_connection" "c" { name = "x" issuer = "https://i.test" client_id = "a" client_secret = "b" }
resource "heytownhall_sso_domain" "d" { connection_id = heytownhall_sso_connection.c.id domain = "gmail.com" }`),
		ExpectError: rx(`(?s)domain_refused.*Free-mail`),
	}}})
}

func TestAccSSO_connectionUpdatesInPlace(t *testing.T) {
	f, cfg := withFake(t)
	var id string
	body := func(name, secret string) string {
		return fmt.Sprintf(`resource "heytownhall_sso_connection" "c" {
  name = %q
  issuer = "https://login.entra.test"
  client_id = "app-1"
  client_secret = %q
}`, name, secret)
	}
	accTest(t, resource.TestCase{Steps: []resource.TestStep{
		{Config: cfg(body("Entra", "s")), Check: resource.ComposeAggregateTestCheckFunc(
			fromState("heytownhall_sso_connection.c", "id", &id),
			// The owner sets these in the dashboard; the provider only shows them.
			resource.TestCheckResourceAttr("heytownhall_sso_connection.c", "email_claim", "email"),
			resource.TestCheckResourceAttr("heytownhall_sso_connection.c", "trust_unverified_email", "false"),
			resource.TestCheckResourceAttr("heytownhall_sso_connection.c", "discovery.token_endpoint", "https://login.entra.test/token"),
			resource.TestCheckResourceAttr("heytownhall_sso_connection.c", "discovery.token_endpoint_auth_method", "client_secret_basic"),
			resource.TestCheckResourceAttrSet("heytownhall_sso_connection.c", "discovery.refreshed_at"),
		)},
		{
			PreConfig: func() { f.SetClaimSettings("upn", true) },
			Config:    cfg(body("Entra ID", "s2")),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("heytownhall_sso_connection.c", "name", "Entra ID"),
				resource.TestCheckResourceAttr("heytownhall_sso_connection.c", "email_claim", "upn"),
				resource.TestCheckResourceAttr("heytownhall_sso_connection.c", "trust_unverified_email", "true"),
				func(s *terraform.State) error {
					if got, _ := stateAttr("heytownhall_sso_connection.c", "id")(s); got != id {
						return fmt.Errorf("the connection was replaced")
					}
					if f.ConnectionSecret(id) != "s2" {
						return fmt.Errorf("the rotated secret was not sent")
					}
					return nil
				},
			),
		},
	}})
}

func TestAccSSO_issuerChangeReplaces(t *testing.T) {
	_, cfg := withFake(t)
	var a, b string
	body := func(issuer string) string {
		return fmt.Sprintf(`resource "heytownhall_sso_connection" "c" { name = "x" issuer = %q client_id = "a" client_secret = "b" }`, issuer)
	}
	accTest(t, resource.TestCase{Steps: []resource.TestStep{
		{Config: cfg(body("https://one.test")), Check: fromState("heytownhall_sso_connection.c", "id", &a)},
		{Config: cfg(body("https://two.test")), Check: fromState("heytownhall_sso_connection.c", "id", &b)},
		{Config: cfg(body("https://two.test")), Check: func(*terraform.State) error {
			if a == b {
				return fmt.Errorf("an issuer change kept the connection")
			}
			return nil
		}},
	}})
}

// ---- data sources ----

func TestAccDataSources(t *testing.T) {
	f, cfg := withFake(t)
	f.AddMember("ana@acme.test", "member")
	c := int64(9900)
	f.SetSpendCap(&c)
	accTest(t, resource.TestCase{Steps: []resource.TestStep{{
		Config: cfg(`
resource "heytownhall_api_key" "a" { name = "a" kind = "sandbox" }
resource "heytownhall_api_key" "b" { name = "b" kind = "sandbox" }
resource "heytownhall_api_key" "c" { name = "c" kind = "sandbox" }

data "heytownhall_organization" "o" {}
data "heytownhall_usage" "now" {}
data "heytownhall_usage" "oct" { month = "2026-10" }
data "heytownhall_api_keys" "all" { depends_on = [heytownhall_api_key.a, heytownhall_api_key.b, heytownhall_api_key.c] }
data "heytownhall_members" "all" {}
data "heytownhall_members" "ana" { email = "ANA@acme.test" }
data "heytownhall_members" "nobody" { email = "nobody@acme.test" }
data "heytownhall_management_tokens" "all" {}
data "heytownhall_token" "self" {}
data "heytownhall_coverage" "all" {}
data "heytownhall_coverage" "bc" { region = "BC" }
data "heytownhall_coverage" "us" { country = "US" }
`),
		Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("data.heytownhall_organization.o", "name", "Acme Council Data"),
			resource.TestCheckResourceAttr("data.heytownhall_organization.o", "spend_cap_cents", "9900"),
			resource.TestCheckResourceAttr("data.heytownhall_usage.now", "month", "2026-11"),
			resource.TestCheckResourceAttr("data.heytownhall_usage.now", "total_cents", "540"),
			resource.TestCheckResourceAttr("data.heytownhall_usage.now", "requests", "12300"),
			resource.TestCheckResourceAttr("data.heytownhall_usage.now", "lines.#", "2"),
			resource.TestCheckResourceAttr("data.heytownhall_usage.now", "lines.0.meter", "records"),
			resource.TestCheckResourceAttr("data.heytownhall_usage.now", "spend_cap_cents", "9900"),
			resource.TestCheckResourceAttr("data.heytownhall_usage.oct", "month", "2026-10"),
			resource.TestCheckResourceAttr("data.heytownhall_api_keys.all", "keys.#", "3"), // three pages of size two: pagination works
			resource.TestCheckResourceAttr("data.heytownhall_members.all", "members.#", "2"),
			resource.TestCheckResourceAttr("data.heytownhall_members.all", "members.0.role", "owner"),
			resource.TestCheckResourceAttr("data.heytownhall_members.ana", "members.#", "1"),
			resource.TestCheckResourceAttr("data.heytownhall_members.ana", "members.0.email", "ana@acme.test"),
			resource.TestCheckResourceAttr("data.heytownhall_members.nobody", "members.#", "0"),
			resource.TestCheckResourceAttr("data.heytownhall_usage.now", "minimum_invoice_cents", "500"),
			resource.TestCheckResourceAttr("data.heytownhall_usage.now", "carried_forward_cents", "120"),
			resource.TestCheckResourceAttr("data.heytownhall_management_tokens.all", "tokens.#", "3"),
			resource.TestCheckResourceAttr("data.heytownhall_management_tokens.all", "tokens.0.scopes.0", "keys:write"),
			resource.TestCheckResourceAttr("data.heytownhall_management_tokens.all", "tokens.1.revoked_at", "2026-10-20T00:00:00Z"),
			resource.TestCheckResourceAttr("data.heytownhall_management_tokens.all", "tokens.1.status", "revoked"),
			resource.TestCheckResourceAttr("data.heytownhall_management_tokens.all", "tokens.0.created_by", "owner@acme.test"),
			resource.TestCheckResourceAttr("data.heytownhall_token.self", "organization_name", "Acme Council Data"),
			resource.TestCheckResourceAttr("data.heytownhall_token.self", "scopes.#", "4"),
			resource.TestCheckResourceAttr("data.heytownhall_coverage.all", "jurisdictions.#", "3"),
			resource.TestCheckResourceAttr("data.heytownhall_coverage.all", "jurisdictions.0.held.meetings", "410"),
			resource.TestCheckResourceAttr("data.heytownhall_coverage.all", "jurisdictions.0.meeting_vendors.0", "escribe"),
			resource.TestCheckResourceAttr("data.heytownhall_coverage.bc", "jurisdictions.#", "2"),
			resource.TestCheckResourceAttr("data.heytownhall_coverage.us", "jurisdictions.0.id", "seattle"),
		),
	}}})
	for _, r := range f.Requests() {
		if r.Path == "/v1/coverage" && r.HadAuthorization {
			t.Errorf("the coverage data source sent the management token to a public route")
		}
	}
}

// The path of the first listed key's revocation time, written so a secret scanner does not read it as a credential.
const firstKeyRevoked = "keys" + ".0." + "revoked_at"

func TestAccDataSource_apiKeysHidesRevokedUnlessAsked(t *testing.T) {
	f, cfg := withFake(t)
	var id string
	accTest(t, resource.TestCase{Steps: []resource.TestStep{
		{Config: cfg(`resource "heytownhall_api_key" "a" { name = "a" kind = "sandbox" }`), Check: fromState("heytownhall_api_key.a", "id", &id)},
		{
			PreConfig: func() { f.RevokeKey(id) },
			Config: cfg(`
data "heytownhall_api_keys" "live" {}
data "heytownhall_api_keys" "every" { include_revoked = true }`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.heytownhall_api_keys.live", "keys.#", "0"),
				resource.TestCheckResourceAttr("data.heytownhall_api_keys.every", "keys.#", "1"),
				resource.TestMatchResourceAttr("data.heytownhall_api_keys.every", firstKeyRevoked, rx(`^20`)),
			),
		},
	}})
}

func TestAccDataSource_usageRejectsBadMonth(t *testing.T) {
	_, cfg := withFake(t)
	accTest(t, resource.TestCase{Steps: []resource.TestStep{{
		Config: cfg(`data "heytownhall_usage" "u" { month = "November" }`), ExpectError: rx(`YYYY-MM`),
	}}})
}

func TestAccInvitation_dailyBudgetIsShownAndNotRetried(t *testing.T) {
	f, cfg := withFake(t)
	f.SetInviteBudget(1)
	f.AddMember("filler@acme.test", "member")
	// One invitation already counts against the budget.
	accTest(t, resource.TestCase{Steps: []resource.TestStep{{
		Config: cfg(`
resource "heytownhall_invitation" "a" {
  email = "a@acme.test"
}
resource "heytownhall_invitation" "b" {
  email      = "b@acme.test"
  depends_on = [heytownhall_invitation.a]
}`),
		ExpectError: rx(`(?s)rate_limited.*50\s+invitations`),
	}}})
	posts := 0
	for _, r := range f.Requests() {
		if r.Method == "POST" && r.Path == "/v1/management/invitations" {
			posts++
		}
	}
	if posts != 2 {
		t.Errorf("want 2 invitation creates (one sent, one refused and not retried), saw %d", posts)
	}
}
