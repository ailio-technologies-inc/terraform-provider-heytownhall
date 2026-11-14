resource "heytownhall_api_key" "staging" {
  name = "staging"
  kind = "sandbox"
}

# The secret is known only because Terraform created the key. It is kept in the state.
output "staging_key" {
  value     = heytownhall_api_key.staging.secret
  sensitive = true
}
