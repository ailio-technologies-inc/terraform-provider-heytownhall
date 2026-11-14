variable "okta_client_secret" {
  type      = string
  sensitive = true
}

resource "heytownhall_sso_connection" "okta" {
  name          = "Okta"
  issuer        = "https://acme.okta.com"
  client_id     = "0oa1b2c3d4e5f6g7h8i9"
  client_secret = var.okta_client_secret
}

# Register this redirect URI with Okta.
output "redirect_uri" {
  value = heytownhall_sso_connection.okta.redirect_uri
}
