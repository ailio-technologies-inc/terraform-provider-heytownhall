# Terraform provider for Hey Town Hall Data

Manage a [Hey Town Hall Data](https://data.heytownhall.com) organisation as code: API keys, team members and invitations, the monthly spending cap, the organisation's name, and single sign-on (OpenID Connect) connections with their domains.

The provider talks to the management API at `https://data.heytownhall.com/v1/management`. It is not the data API: it does not read meetings or bylaws.

Status: pre-release. The provider is not on the Terraform Registry yet, and the management API it calls is not yet live at data.heytownhall.com, so `terraform init` will not find it today. This repository is where it is being built.

## Install

```hcl
terraform {
  required_providers {
    heytownhall = {
      source  = "ailio-technologies-inc/heytownhall"
      version = "~> 0.1"
    }
  }
}

provider "heytownhall" {}
```

Then `terraform init`. Terraform 1.0 or later. The provider uses plugin protocol 6.

## Authenticate

The provider uses a management token, which starts with `htd_mgmt_`. An organisation owner creates one in the dashboard and picks its scopes:

| Scope | Lets the token |
| --- | --- |
| `keys:read`, `keys:write` | list, create, rename and revoke API keys |
| `members:read`, `members:write` | list members, change roles, remove members, send and cancel invitations |
| `settings:read`, `settings:write` | read and change the organisation's name and spending cap |
| `sso:read`, `sso:write` | read and change SSO connections and domains (turning a connection on needs `members:write` as well) |

Write includes read. Give a token only the scopes for the resources you manage. `sso:write` is as powerful as being able to add people (it can point sign-in at another identity provider), so treat it that way. The organisation, usage and token list are readable by every token.

A token stops working when it is revoked, when it expires, or when the owner who created it is no longer an owner.

Set the token in the environment, so it stays out of your files and your state:

```sh
export HEYTOWNHALL_MANAGEMENT_TOKEN=htd_mgmt_...
```

Or pass it with the `token` argument from a sensitive variable. A data API key (`htd_test_...`, `htd_live_...`) does not work and the provider says so.

| Argument | Environment variable | Default |
| --- | --- | --- |
| `token` (sensitive) | `HEYTOWNHALL_MANAGEMENT_TOKEN` | none, required |
| `endpoint` | `HEYTOWNHALL_ENDPOINT` | `https://data.heytownhall.com` |

`endpoint` is for testing. It must use https, except for `localhost`.

The API allows 120 requests a minute per token. The provider waits and retries on `429` (it honours `Retry-After`) and on `502`, `503` and `504`. Every create carries an `Idempotency-Key` that is made once and reused on its retries, so a retry never makes a second key or a second invitation.

## What is managed

| Resource | Manages | Destroy |
| --- | --- | --- |
| `heytownhall_api_key` | a data API key | revokes the key |
| `heytownhall_member` | the role of an existing member | removes the person from the organisation |
| `heytownhall_invitation` | an invitation by email | cancels it if pending; does nothing once accepted |
| `heytownhall_spend_cap` | the monthly spending cap | removes the cap |
| `heytownhall_organization` | the organisation's name | removes it from the state only |
| `heytownhall_sso_connection` | an OpenID Connect connection | deletes it |
| `heytownhall_sso_domain` | an email domain on a connection, and its DNS record | removes it |
| `heytownhall_sso_domain_verification` | nothing: waits until the domain is verified | nothing |
| `heytownhall_sso_activation` | whether a connection is on, and enforced | turns it off |

| Data source | Reads |
| --- | --- |
| `heytownhall_organization` | the organisation's id and name, and its spending cap |
| `heytownhall_token` | the token in use: its scopes, expiry and organisation |
| `heytownhall_usage` | a month's bill to date |
| `heytownhall_api_keys` | the keys (never their secrets) |
| `heytownhall_members` | the members |
| `heytownhall_management_tokens` | the tokens' names, scopes and dates (never the tokens) |
| `heytownhall_coverage` | the public coverage list (no token is sent) |

## What is not managed

- **Owners.** A management token can never create, promote, demote or remove an owner, because owners make tokens and a token making an owner would be escalating itself. Manage owners in the dashboard. Pointing `heytownhall_member` at an owner fails with the API's `owner_protected` error. Only the role `member` can be assigned through the API.
- **Management tokens.** They are made and revoked in the dashboard. The provider can list them (`heytownhall_management_tokens`) and read the one it uses (`heytownhall_token`).
- **Card, invoices, and deleting the organisation.** Dashboard only.
- **People who have not accepted.** Terraform can send an invitation; only the person can accept it. See the [guide](docs/guides/invitations-and-members.md).
- **SAML and SCIM.** The API has neither.

## Things to know

- **Secrets are in the state.** The API shows an API key's secret once, when the key is created, so the provider keeps it in `heytownhall_api_key.secret` (marked sensitive) in the state. The same goes for `heytownhall_sso_connection.client_secret`, which you write in the configuration. Keep the state in an encrypted backend with restricted access.
- **A key whose secret was lost is revoked.** If a create reaches the API but its answer is lost, the retry gets a stored answer with no secret. The provider revokes that key and fails, and the next apply makes a new one.
- **The client secret cannot be drift-checked.** The API never returns it. Terraform sends it on create and when the configuration value changes. A change in the dashboard is not seen.
- **A key revoked outside Terraform** leaves the state on the next refresh, and the next apply creates a new one.
- **Invitation limits.** An organisation can hold only so many pending invitations and send only so many a day. The API refuses past either, and the provider shows its error and does not retry it.
- **Invitations.** Accepted ones stay in the state with `status = "accepted"` and plan nothing. Expired ones leave the state, and the next apply invites again.
- **SSO is five resources, not one**, because the API turns a connection on only after a domain is verified, and a domain needs the connection. See the [single sign-on guide](docs/guides/single-sign-on.md).

## Import

| Resource | Import id |
| --- | --- |
| `heytownhall_api_key` | the key's id. `secret` stays null: the API never shows it again. |
| `heytownhall_member` | the user id |
| `heytownhall_invitation` | the invitation's id |
| `heytownhall_spend_cap` | the organisation's id |
| `heytownhall_organization` | the organisation's id |
| `heytownhall_sso_connection` | the connection's id. `client_secret` is unknown; the first apply sets it from your configuration. |
| `heytownhall_sso_domain` | `<connection id>/<domain id>` |
| `heytownhall_sso_activation` | the connection's id |

`heytownhall_sso_domain_verification` is not imported: add it to the configuration and apply, and it verifies the domain.

## A complete example

[`examples/complete/main.tf`](examples/complete/main.tf): keys for two environments, two invitations, a spending cap, and SSO whose DNS record is written by a Cloudflare resource for one domain and by a Route 53 resource for another.

## Develop

```sh
make test      # unit tests; acceptance tests are skipped
make testacc   # acceptance tests: real Terraform against an in-process fake of the API
make docs      # regenerate docs/ from the schemas, examples/ and templates/
make lint      # golangci-lint
make testlive  # acceptance tests against a real API you name by environment variable (see below)
```

`make testacc` needs the Terraform CLI. It uses no network and no credentials. The fake API in `internal/fakeapi` follows the management API's design: owner protection, revoked keys that read back, idempotent replays with a null secret, cursor pagination, rate limiting, and domain proof by a TXT record.

`make testlive` runs the `TestLive_*` acceptance tests against a real management API: whichever one `HEYTOWNHALL_ENDPOINT` and `HEYTOWNHALL_MANAGEMENT_TOKEN` name (the token needs every scope). They rename the organisation, set and remove its spending cap, and create and revoke keys and invitations, so use a scratch organisation and also set `HEYTOWNHALL_TEST_ALLOW_WRITES=1`. Set `HEYTOWNHALL_TEST_SSO_ISSUER`, `HEYTOWNHALL_TEST_SSO_CLIENT_ID` and `HEYTOWNHALL_TEST_SSO_CLIENT_SECRET` as well to run the single sign-on refusals. Accepting an invitation and publishing DNS cannot be done from outside, so those paths are covered only by the fake-API tests.

Layout: `internal/client` (HTTP client), `internal/provider` (resources and data sources), `internal/fakeapi` (the fake), `examples/`, `templates/` (guides for the docs), `docs/` (generated).

## Licence

[MPL-2.0](LICENSE), the licence of Terraform and of most providers. It asks that changes to this provider's own files stay open, and puts no condition on how you use the provider or on your Terraform configurations.
