data "heytownhall_members" "all" {}

output "owner_emails" {
  value = [for m in data.heytownhall_members.all.members : m.email if m.role == "owner"]
}
