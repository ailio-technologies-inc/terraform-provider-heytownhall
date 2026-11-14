resource "heytownhall_sso_activation" "okta" {
  connection_id = heytownhall_sso_connection.okta.id
  enabled       = true
  enforced      = false # set true once everyone has signed in through SSO once

  depends_on = [heytownhall_sso_domain_verification.acme]
}
