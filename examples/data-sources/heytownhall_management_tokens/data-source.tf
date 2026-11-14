data "heytownhall_management_tokens" "all" {}

# Tokens that can still be used and have not been used for a while are worth revoking in the dashboard.
output "active_token_names" {
  value = [for t in data.heytownhall_management_tokens.all.tokens : t.name if t.revoked_at == null]
}
