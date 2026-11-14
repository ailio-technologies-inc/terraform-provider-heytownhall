data "heytownhall_api_keys" "active" {}

output "key_names" {
  value = [for k in data.heytownhall_api_keys.active.keys : "${k.name} (${k.kind})"]
}
