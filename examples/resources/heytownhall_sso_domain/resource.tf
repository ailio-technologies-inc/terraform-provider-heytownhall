resource "heytownhall_sso_domain" "acme" {
  connection_id = heytownhall_sso_connection.okta.id
  domain        = "acme.com"
}

# Publish this record in DNS, then confirm it with heytownhall_sso_domain_verification.
output "dns_record" {
  value = heytownhall_sso_domain.acme.verification_record
}
