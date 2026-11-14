---
page_title: "Single sign-on"
subcategory: ""
description: |-
  Connect an OpenID Connect identity provider, prove the email domain in DNS, turn the connection on, and require it.
---

# Single sign-on

Single sign-on takes five resources, in this order. Each one depends on the one before, so one `terraform apply` does all of it.

1. `heytownhall_sso_connection` stores the identity provider's issuer, client id and client secret. The API reads the provider's discovery document when it is created.
2. `heytownhall_sso_domain` claims an email domain for the connection. Its `verification_record` attribute is the DNS record that proves you control the domain.
3. A DNS resource from your DNS provider's Terraform provider publishes that record.
4. `heytownhall_sso_domain_verification` asks the API to look for the record, and waits until it is found.
5. `heytownhall_sso_activation` turns the connection on, and optionally requires it.

```terraform
resource "heytownhall_sso_connection" "okta" {
  name          = "Okta"
  issuer        = "https://acme.okta.com"
  client_id     = "0oa1b2c3d4e5f6g7h8i9"
  client_secret = var.okta_client_secret
}

resource "heytownhall_sso_domain" "acme" {
  connection_id = heytownhall_sso_connection.okta.id
  domain        = "acme.com"
}

resource "cloudflare_dns_record" "sso_verify" {
  zone_id = var.cloudflare_zone_id
  name    = heytownhall_sso_domain.acme.verification_record.name
  type    = heytownhall_sso_domain.acme.verification_record.type
  content = heytownhall_sso_domain.acme.verification_record.value
  ttl     = 120
}

resource "heytownhall_sso_domain_verification" "acme" {
  sso_domain_id = heytownhall_sso_domain.acme.id
  depends_on    = [cloudflare_dns_record.sso_verify]
}

resource "heytownhall_sso_activation" "okta" {
  connection_id = heytownhall_sso_connection.okta.id
  depends_on    = [heytownhall_sso_domain_verification.acme]
}
```

Register the connection's `redirect_uri` with your identity provider as its only redirect URI.

## Scopes

The token needs `sso:write` for the connection and the domain. `heytownhall_sso_activation` (turning the connection on) also needs `members:write`, because people who sign in through single sign-on join the organisation as members. Give the token both.

## What an owner sets in the dashboard

`email_claim` (which ID token claim holds the address) and `trust_unverified_email` (accept an address with no `email_verified` claim, which Microsoft Entra needs) can be changed only by an owner signed in to the dashboard, not by a management token. `heytownhall_sso_connection` shows both and does not set them. If your provider is Entra, set them in the dashboard before you enable the connection.

## People who were removed

A person removed from the organisation cannot rejoin through single sign-on until they are invited again. After you remove a `heytownhall_member`, add a `heytownhall_invitation` for that address to let them back in.

## Why activation is a separate resource

The API turns a connection on only when it has a verified domain. A verified domain needs the connection to exist. If `enabled` were an argument of the connection, the connection would wait for the verification, the verification for the domain, and the domain for the connection: a loop that Terraform refuses. Putting `enabled` and `enforced` in `heytownhall_sso_activation` makes the chain a line.

## Enforcing

`enforced = true` means people with an address at a verified domain of the connection can sign in only through it. Turn it on after people have signed in through SSO at least once. An owner can still sign in with a password; each such sign-in is recorded and every owner is told by email, so a broken identity provider does not lock the organisation out.

## The client secret

The API never returns the client secret, so Terraform cannot detect that it was changed in the dashboard. Terraform sends the secret when it creates the connection and whenever the value in your configuration changes. To rotate it, change the variable and apply.

## Timing

DNS changes take time to spread. `heytownhall_sso_domain_verification` checks every ten seconds and gives up after ten minutes. Change that with `timeouts = { create = "30m" }`. If it gives up, apply again: the resource is not in the state, so it waits again.

## Keep the DNS record

Turning the connection on, and enforcing it, make the API check the TXT record again. If the record is gone, the API answers `domain_not_verified` and the domain reads as unverified, and the next plan verifies it again. Leave the record published for as long as the connection is in use.

## Removing

Destroying `heytownhall_sso_activation` turns the connection off and stops enforcing it. Destroying a domain or the connection removes it.
