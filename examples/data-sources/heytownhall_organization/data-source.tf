data "heytownhall_organization" "this" {}

output "organization_name" {
  value = data.heytownhall_organization.this.name
}
