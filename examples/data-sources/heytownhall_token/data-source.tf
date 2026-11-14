data "heytownhall_token" "self" {}

# Warn early when the token lacks a scope this configuration needs.
# Turning an SSO connection on needs both sso:write and members:write.
check "token_can_manage_keys_and_sso" {
  assert {
    condition = alltrue([
      for s in ["keys:write", "sso:write", "members:write"] : contains(data.heytownhall_token.self.scopes, s)
    ])
    error_message = "The management token needs the keys:write, sso:write and members:write scopes."
  }
}
