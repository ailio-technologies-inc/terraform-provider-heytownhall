# A whole organisation as code: keys for two environments, two invitations, a spending cap,
# and single sign-on whose DNS proof is written by Cloudflare for one domain and by Route 53 for another.
#
# Providers other than heytownhall need their own credentials (CLOUDFLARE_API_TOKEN, AWS_*).

terraform {
  required_providers {
    heytownhall = {
      source  = "ailio-technologies-inc/heytownhall"
      version = "~> 0.1"
    }
    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = "~> 5.0"
    }
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }
}

variable "okta_client_secret" {
  type      = string
  sensitive = true
}

variable "cloudflare_zone_id" {
  description = "The zone for acme.com."
  type        = string
}

variable "route53_zone_id" {
  description = "The hosted zone for acme.org."
  type        = string
}

provider "heytownhall" {} # HEYTOWNHALL_MANAGEMENT_TOKEN
provider "cloudflare" {}
provider "aws" {
  region = "us-east-1"
}

# ---- the organisation ----

resource "heytownhall_organization" "this" {
  name = "Acme Civic Research"
}

resource "heytownhall_spend_cap" "monthly" {
  cents = 50000 # 500 US dollars
}

# ---- API keys for two environments ----

resource "heytownhall_api_key" "staging" {
  name = "staging"
  kind = "sandbox"
}

resource "heytownhall_api_key" "production" {
  name = "production"
  kind = "live"
}

output "staging_key" {
  value     = heytownhall_api_key.staging.secret
  sensitive = true
}

output "production_key" {
  value     = heytownhall_api_key.production.secret
  sensitive = true
}

# ---- people ----

resource "heytownhall_invitation" "people" {
  for_each = toset(["ana@acme.com", "ben@acme.org"])

  email = each.key
  role  = "member"
}

# ---- single sign-on ----

resource "heytownhall_sso_connection" "okta" {
  name          = "Okta"
  issuer        = "https://acme.okta.com"
  client_id     = "0oa1b2c3d4e5f6g7h8i9"
  client_secret = var.okta_client_secret
}

resource "heytownhall_sso_domain" "acme_com" {
  connection_id = heytownhall_sso_connection.okta.id
  domain        = "acme.com"
}

resource "heytownhall_sso_domain" "acme_org" {
  connection_id = heytownhall_sso_connection.okta.id
  domain        = "acme.org"
}

# The record for acme.com, written by Cloudflare.
resource "cloudflare_dns_record" "sso_verify_acme_com" {
  zone_id = var.cloudflare_zone_id
  name    = heytownhall_sso_domain.acme_com.verification_record.name
  type    = heytownhall_sso_domain.acme_com.verification_record.type
  content = heytownhall_sso_domain.acme_com.verification_record.value
  ttl     = 120
}

# The record for acme.org, written by Route 53. Route 53 wants TXT values in double quotes.
resource "aws_route53_record" "sso_verify_acme_org" {
  zone_id = var.route53_zone_id
  name    = heytownhall_sso_domain.acme_org.verification_record.name
  type    = heytownhall_sso_domain.acme_org.verification_record.type
  ttl     = 60
  records = ["\"${heytownhall_sso_domain.acme_org.verification_record.value}\""]
}

# Each verification waits until the API finds its record.
resource "heytownhall_sso_domain_verification" "acme_com" {
  sso_domain_id = heytownhall_sso_domain.acme_com.id
  depends_on    = [cloudflare_dns_record.sso_verify_acme_com]
}

resource "heytownhall_sso_domain_verification" "acme_org" {
  sso_domain_id = heytownhall_sso_domain.acme_org.id
  depends_on    = [aws_route53_record.sso_verify_acme_org]
}

# Turn the connection on once at least one domain is verified. Enforce it later, after people have signed in.
resource "heytownhall_sso_activation" "okta" {
  connection_id = heytownhall_sso_connection.okta.id
  enabled       = true
  enforced      = false

  depends_on = [
    heytownhall_sso_domain_verification.acme_com,
    heytownhall_sso_domain_verification.acme_org,
  ]
}
